// Package speech writes recordings out: whisper.cpp on this machine (the
// default, nothing leaves the box) or an OpenAI-compatible transcription API
// with the person's own key. ffmpeg turns any recording into the 16 kHz WAV
// whisper reads and into an AAC copy every browser plays.
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Segment is one stretch of speech, in milliseconds from the start.
type Segment struct {
	Start   int64  `json:"s"`
	End     int64  `json:"e"`
	Text    string `json:"t"`
	Speaker string `json:"speaker,omitempty"` // meetings: You | Others
}

// Result is what a recording said.
type Result struct {
	Text     string    `json:"text"`
	Language string    `json:"language"`
	Segments []Segment `json:"segments"`
	Engine   string    `json:"engine"`
}

// Model is one speech model Voice can use: OpenAI's Whisper (about 99 languages) or NVIDIA's
// Parakeet (25 European languages, many times faster), both run by whisper.cpp.
type Model struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Engine string `json:"engine"` // whisper | parakeet
	File   string `json:"file"`
	URL    string `json:"-"`     // where it is downloaded from ("" = MODEL_BASE_URL + File)
	Bytes  int64  `json:"bytes"` // the download, roughly
	Note   string `json:"note"`
}

// ParakeetModel is the fastest model: Parakeet TDT 0.6B v3, 8-bit.
const ParakeetModel = "parakeet-tdt-0.6b-v3-q8_0"

// Models are the models offered in Settings.
var Models = []Model{
	{ID: ParakeetModel, Name: "Fastest", Engine: "parakeet", File: "ggml-parakeet-tdt-0.6b-v3-q8_0.bin",
		URL: "https://huggingface.co/ggml-org/parakeet-GGUF/resolve/main/ggml-parakeet-tdt-0.6b-v3-q8_0.bin", Bytes: 668757119,
		Note: "Parakeet: about ten times faster than Best and as accurate in 25 European languages, Croatian among them (Bosnian and Serbian come out in Latin letters). It finds the language itself and doesn't use the names to expect."},
	{ID: "base", Name: "Fast", Engine: "whisper", File: "ggml-base.bin", Bytes: 147951465, Note: "Whisper base: quick and rough; fine for clear English."},
	{ID: "small", Name: "Good", Engine: "whisper", File: "ggml-small.bin", Bytes: 487601967, Note: "Whisper small: good in most languages and about three times faster than Best."},
	{ID: "large-v3-turbo-q5_0", Name: "Best", Engine: "whisper", File: "ggml-large-v3-turbo-q5_0.bin", Bytes: 574041195,
		Note: "Whisper large-v3-turbo: the most accurate in about 99 languages, uses the names to expect; slow on a small server (about as long as the recording)."},
}

// DefaultModel is the model used until an admin picks another.
const DefaultModel = "large-v3-turbo-q5_0"

// ModelByID finds a model ("" = the default).
func ModelByID(id string) (Model, bool) {
	if id == "" {
		id = DefaultModel
	}
	for _, m := range Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Local runs ffmpeg and whisper.cpp (and its parakeet-cli) on this machine.
type Local struct {
	Whisper   string // whisper-cli
	Parakeet  string // parakeet-cli
	FFmpeg    string
	Dir       string // where the models are kept
	BaseURL   string // where models are downloaded from
	UserAgent string
	Threads   int // dictation: someone is waiting
	// BackThreads is for memos and meetings, written out in the background at
	// the lowest priority so the box stays free for everything else.
	BackThreads int

	mu   sync.Mutex
	dl   map[string]*Download
	runs sync.Mutex // one whisper at a time: it uses every core it is given
}

// Download is a model being fetched.
type Download struct {
	Model string `json:"model"`
	Got   int64  `json:"got"`
	Total int64  `json:"total"`
	Error string `json:"error"`
	Done  bool   `json:"done"`
	done  chan struct{}
}

// DefaultThreads is half the cores, 1 to 4: past 4 whisper.cpp gets no faster
// on a 6-core i5 (6 threads took 850% CPU and were slower than 4).
func DefaultThreads() int {
	n := runtime.NumCPU() / 2
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	return n
}

// DefaultBackThreads is one fewer than DefaultThreads, at least 1.
func DefaultBackThreads() int {
	if n := DefaultThreads() - 1; n > 1 {
		return n
	}
	return 1
}

type backgroundKey struct{}

// Background marks work nobody is waiting for (memos, meetings): it runs on
// BackThreads at the lowest CPU priority.
func Background(ctx context.Context) context.Context {
	return context.WithValue(ctx, backgroundKey{}, true)
}

func isBackground(ctx context.Context) bool {
	b, _ := ctx.Value(backgroundKey{}).(bool)
	return b
}

// threads is how many threads a run gets.
func (l *Local) threads(ctx context.Context) int {
	if isBackground(ctx) {
		if l.BackThreads > 0 {
			return l.BackThreads
		}
		return DefaultBackThreads()
	}
	if l.Threads > 0 {
		return l.Threads
	}
	return DefaultThreads()
}

// command runs bin, under `nice -n 19` when the work is in the background.
func command(ctx context.Context, bin string, args ...string) *exec.Cmd {
	if isBackground(ctx) {
		if nice := look("nice"); nice != "" {
			return exec.CommandContext(ctx, nice, append([]string{"-n", "19", bin}, args...)...)
		}
	}
	return exec.CommandContext(ctx, bin, args...)
}

func look(bin string) string {
	if bin == "" {
		return ""
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return ""
	}
	return p
}

// HasWhisper reports whether whisper.cpp is installed.
func (l *Local) HasWhisper() bool { return l != nil && look(l.Whisper) != "" }

// HasFFmpeg reports whether ffmpeg is installed.
func (l *Local) HasFFmpeg() bool { return l != nil && look(l.FFmpeg) != "" }

// ModelPath is where a model's file lives.
func (l *Local) ModelPath(m Model) string { return filepath.Join(l.Dir, m.File) }

// ModelReady reports whether a model is downloaded and looks like one.
func (l *Local) ModelReady(m Model) bool {
	f, err := os.Open(l.ModelPath(m))
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Size() > 1<<20 && (m.Engine == "parakeet" || string(magic[:]) == "lmgg")
}

// DownloadState is the model's current download, if any.
func (l *Local) DownloadState(id string) *Download {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d, ok := l.dl[id]; ok {
		c := *d
		return &c
	}
	return nil
}

// Fetch downloads a model (once, however many ask) and waits for it.
func (l *Local) Fetch(ctx context.Context, m Model) error {
	if l.ModelReady(m) {
		return nil
	}
	l.mu.Lock()
	if l.dl == nil {
		l.dl = map[string]*Download{}
	}
	d, ok := l.dl[m.ID]
	if !ok || (d.Done && d.Error != "") {
		d = &Download{Model: m.ID, Total: m.Bytes, done: make(chan struct{})}
		l.dl[m.ID] = d
		go l.fetch(m, d)
	}
	l.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.done:
	}
	if d.Error != "" {
		return errors.New(d.Error)
	}
	return nil
}

// StartFetch begins a download in the background.
func (l *Local) StartFetch(m Model) {
	go func() { _ = l.Fetch(context.Background(), m) }()
}

func (l *Local) fetch(m Model, d *Download) {
	err := l.fetchFile(m, d)
	l.mu.Lock()
	d.Done = true
	if err != nil {
		d.Error = "the speech model could not be downloaded: " + err.Error()
	}
	l.mu.Unlock()
	close(d.done)
}

type counter struct {
	l *Local
	d *Download
}

func (c counter) Write(p []byte) (int, error) {
	c.l.mu.Lock()
	c.d.Got += int64(len(p))
	c.l.mu.Unlock()
	return len(p), nil
}

func (l *Local) fetchFile(m Model, d *Download) error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	src := m.URL
	if src == "" {
		src = strings.TrimRight(l.BaseURL, "/") + "/" + m.File
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", l.UserAgent)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if res.ContentLength > 0 {
		l.mu.Lock()
		d.Total = res.ContentLength
		l.mu.Unlock()
	}
	part := l.ModelPath(m) + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.MultiWriter(f, counter{l, d}), res.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Rename(part, l.ModelPath(m)); err != nil {
		return err
	}
	if !l.ModelReady(m) {
		_ = os.Remove(l.ModelPath(m))
		return errors.New("the file is not a whisper.cpp model")
	}
	return nil
}

// RemoveModel deletes a downloaded model.
func (l *Local) RemoveModel(m Model) error {
	l.mu.Lock()
	delete(l.dl, m.ID)
	l.mu.Unlock()
	err := os.Remove(l.ModelPath(m))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l *Local) ffmpeg(ctx context.Context, args ...string) error {
	bin := look(l.FFmpeg)
	if bin == "" {
		return errors.New("ffmpeg is not installed")
	}
	var stderr bytes.Buffer
	cmd := command(ctx, bin, append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New("the recording could not be read: " + msg)
	}
	return nil
}

// ToWAV decodes any recording into 16 kHz mono 16-bit WAV.
func (l *Local) ToWAV(ctx context.Context, in, out string) error {
	return l.ffmpeg(ctx, "-i", in, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "wav", out)
}

// ToWAVChannels decodes a recording into 16 kHz 16-bit WAV keeping its channels (meetings).
func (l *Local) ToWAVChannels(ctx context.Context, in, out string) error {
	return l.ffmpeg(ctx, "-i", in, "-vn", "-ar", "16000", "-c:a", "pcm_s16le", "-f", "wav", out)
}

// ToM4AStereo is ToM4A keeping both channels (a meeting: you and the call).
func (l *Local) ToM4AStereo(ctx context.Context, in, out string) error {
	return l.ffmpeg(ctx, "-i", in, "-vn", "-ac", "2", "-c:a", "aac", "-b:a", "96k", "-movflags", "+faststart", "-f", "mp4", out)
}

// ToM4A makes the AAC copy every browser plays (Safari plays no WebM/Ogg).
func (l *Local) ToM4A(ctx context.Context, in, out string) error {
	return l.ffmpeg(ctx, "-i", in, "-vn", "-ac", "1", "-c:a", "aac", "-b:a", "64k", "-movflags", "+faststart", "-f", "mp4", out)
}

// Playable reports whether every browser plays this type as it is.
func Playable(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	switch mime {
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac", "audio/mpeg", "audio/mp3":
		return true
	}
	return false
}

var progressRE = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

type whisperJSON struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Offsets struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

// Transcribe writes out a 16 kHz WAV with whisper.cpp. lang is a whisper
// language code or "" (find out); prompt is words to expect (names, places).
// progress gets 0–100 while it works.
func (l *Local) Transcribe(ctx context.Context, wav string, m Model, lang, prompt string, progress func(int)) (*Result, error) {
	if m.Engine == "parakeet" {
		return l.parakeet(ctx, wav, m)
	}
	bin := look(l.Whisper)
	if bin == "" {
		return nil, errors.New("whisper.cpp is not installed here")
	}
	if !l.ModelReady(m) {
		return nil, errors.New("the speech model is not downloaded yet")
	}
	l.runs.Lock()
	defer l.runs.Unlock()
	if lang == "" {
		lang = "auto"
	}
	threads := l.threads(ctx)
	outBase := strings.TrimSuffix(wav, filepath.Ext(wav)) + "-words"
	defer os.Remove(outBase + ".json")
	args := []string{"-m", l.ModelPath(m), "-f", wav, "-l", lang, "-t", strconv.Itoa(threads), "-oj", "-of", outBase, "-pp", "-np", "-sns"}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, "--prompt", p)
	}
	cmd := command(ctx, bin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	var tail bytes.Buffer
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	for {
		n, rerr := stderr.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if tail.Len() > 8000 {
				tail.Reset()
			}
			tail.Write(chunk)
			if progress != nil {
				for _, mm := range progressRE.FindAllSubmatch(chunk, -1) {
					if p, err := strconv.Atoi(string(mm[1])); err == nil {
						progress(p)
					}
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("whisper.cpp stopped: %s", lastLine(tail.String(), err.Error()))
	}
	b, err := os.ReadFile(outBase + ".json")
	if err != nil {
		return nil, fmt.Errorf("whisper.cpp wrote nothing: %w", err)
	}
	var out whisperJSON
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("whisper.cpp output: %w", err)
	}
	res := &Result{Language: out.Result.Language, Engine: "whisper " + m.ID, Segments: []Segment{}}
	for _, t := range out.Transcription {
		txt := strings.TrimSpace(t.Text)
		if txt == "" || isNoise(txt) {
			continue
		}
		res.Segments = append(res.Segments, Segment{Start: t.Offsets.From, End: t.Offsets.To, Text: txt})
	}
	res.Text = JoinSegments(res.Segments)
	return res, nil
}

func lastLine(s, def string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !progressRE.MatchString(l) {
			return l
		}
	}
	return def
}

// isNoise is whisper's way of saying there was nothing to hear.
func isNoise(t string) bool {
	t = strings.ToLower(strings.Trim(t, " .[]()*♪"))
	switch t {
	case "", "blank_audio", "silence", "music", "no speech", "inaudible":
		return true
	}
	return false
}

// JoinSegments is the plain text of the segments: sentences flow on, a long
// pause starts a new paragraph.
func JoinSegments(segs []Segment) string {
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			if s.Start-segs[i-1].End > 2500 {
				b.WriteString("\n\n")
			} else {
				b.WriteString(" ")
			}
		}
		b.WriteString(strings.TrimSpace(s.Text))
	}
	return strings.TrimSpace(b.String())
}

// HasEngine reports whether the program for a model is installed.
func (l *Local) HasEngine(m Model) bool {
	if l == nil {
		return false
	}
	if m.Engine == "parakeet" {
		return look(l.Parakeet) != ""
	}
	return look(l.Whisper) != ""
}

var parakeetSegment = regexp.MustCompile(`^Segment \d+: \[(\d+) -> (\d+)\] "(.*)"$`)

// parakeet writes out a 16 kHz WAV with parakeet-cli (it finds the language itself).
func (l *Local) parakeet(ctx context.Context, wav string, m Model) (*Result, error) {
	bin := look(l.Parakeet)
	if bin == "" {
		return nil, errors.New("parakeet-cli is not installed here")
	}
	if !l.ModelReady(m) {
		return nil, errors.New("the speech model is not downloaded yet")
	}
	l.runs.Lock()
	defer l.runs.Unlock()
	threads := l.threads(ctx)
	var stdout, stderr bytes.Buffer
	cmd := command(ctx, bin, "-m", l.ModelPath(m), "-f", wav, "-t", strconv.Itoa(threads), "-ps")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("parakeet stopped: %s", lastLine(stderr.String(), err.Error()))
	}
	res := ParseParakeet(stdout.String())
	res.Engine = "parakeet " + m.ID
	return res, nil
}

// ParseParakeet reads parakeet-cli's output: the "Segment N: [t0 -> t1] "text"" lines (t in
// hundredths of a second), else the plain text lines.
func ParseParakeet(out string) *Result {
	res := &Result{Segments: []Segment{}}
	var plain []string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "Processing") || strings.HasPrefix(t, "Segments (") ||
			strings.HasPrefix(t, "Tokens [") || strings.HasPrefix(t, "parakeet_") || strings.HasPrefix(t, "load_backend") {
			continue
		}
		if mm := parakeetSegment.FindStringSubmatch(t); mm != nil {
			a, _ := strconv.ParseInt(mm[1], 10, 64)
			b, _ := strconv.ParseInt(mm[2], 10, 64)
			if txt := strings.TrimSpace(mm[3]); txt != "" {
				res.Segments = append(res.Segments, Segment{Start: a * 10, End: b * 10, Text: txt})
			}
			continue
		}
		plain = append(plain, t)
	}
	if len(res.Segments) > 0 {
		res.Text = JoinSegments(res.Segments)
	} else {
		res.Text = strings.TrimSpace(strings.Join(plain, " "))
		if res.Text != "" {
			res.Segments = append(res.Segments, Segment{Text: res.Text})
		}
	}
	return res
}
