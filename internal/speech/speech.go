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
	Start int64  `json:"s"`
	End   int64  `json:"e"`
	Text  string `json:"t"`
}

// Result is what a recording said.
type Result struct {
	Text     string    `json:"text"`
	Language string    `json:"language"`
	Segments []Segment `json:"segments"`
	Engine   string    `json:"engine"`
}

// Model is one whisper.cpp model Voice can use.
type Model struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	File  string `json:"file"`
	Bytes int64  `json:"bytes"` // the download, roughly
	Note  string `json:"note"`
}

// Models are the whisper.cpp models offered in Settings, smallest first.
var Models = []Model{
	{ID: "base", Name: "Fast", File: "ggml-base.bin", Bytes: 147951465, Note: "Quick and rough; fine for clear English."},
	{ID: "small", Name: "Good", File: "ggml-small.bin", Bytes: 487601967, Note: "Good in most languages and about three times faster than Best."},
	{ID: "large-v3-turbo-q5_0", Name: "Best", File: "ggml-large-v3-turbo-q5_0.bin", Bytes: 574041195, Note: "The most accurate, also for Bosnian, Croatian and Serbian."},
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

// Local runs ffmpeg and whisper.cpp on this machine.
type Local struct {
	Whisper   string // whisper-cli
	FFmpeg    string
	Dir       string // where the models are kept
	BaseURL   string // where models are downloaded from
	UserAgent string
	Threads   int

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

// DefaultThreads is half the cores, 1 to 8: whisper is quickest on real cores
// and the box keeps some for everything else.
func DefaultThreads() int {
	n := runtime.NumCPU() / 2
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
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
	return err == nil && fi.Size() > 1<<20 && string(magic[:]) == "lmgg"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(l.BaseURL, "/")+"/"+m.File, nil)
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
	cmd := exec.CommandContext(ctx, bin, append([]string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}, args...)...)
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
	threads := l.Threads
	if threads <= 0 {
		threads = DefaultThreads()
	}
	outBase := strings.TrimSuffix(wav, filepath.Ext(wav)) + "-words"
	defer os.Remove(outBase + ".json")
	args := []string{"-m", l.ModelPath(m), "-f", wav, "-l", lang, "-t", strconv.Itoa(threads), "-oj", "-of", outBase, "-pp", "-np", "-sns"}
	if p := strings.TrimSpace(prompt); p != "" {
		args = append(args, "--prompt", p)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
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
