// Package speakers finds who spoke when in a recording, on this machine: a
// pyannote segmentation model finds the voices in each stretch, a WeSpeaker
// model turns each voice into a voice print, and the prints are grouped into
// the people of the recording. The models run in a small Python program
// (numpy + onnxruntime, both in the Docker image) embedded in this binary.
package speakers

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed speakers.py
var script []byte

// Model is one model file the speaker finder needs.
type Model struct {
	ID     string
	File   string
	URL    string
	SHA256 string
	Bytes  int64
}

// Models: pyannote segmentation 3.0 (MIT) and WeSpeaker ResNet34-LM trained on
// VoxCeleb (Apache 2.0), as ONNX from the sherpa-onnx project.
var Models = []Model{
	{ID: "segmentation", File: "pyannote-segmentation-3.0.onnx", Bytes: 5992913,
		URL:    "https://huggingface.co/csukuangfj/sherpa-onnx-pyannote-segmentation-3-0/resolve/main/model.onnx",
		SHA256: "220ad67ca923bef2fa91f2390c786097bf305bceb5e261d4af67b38e938e1079"},
	{ID: "embedding", File: "wespeaker-voxceleb-resnet34-lm.onnx", Bytes: 26530550,
		URL:    "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/wespeaker_en_voxceleb_resnet34_LM.onnx",
		SHA256: "e9848563da86f263117134dfd7ad63c92355b37de492b55e325400c9d9c39012"},
}

// Tuning, measured on a 38-minute stand-up with seven voices on the call:
// voices of one person score 0.81–0.97 against each other, two people at
// most 0.47.
const (
	Threshold = 0.55 // two groups of voice prints are one person above this
	StepS     = 5.0  // seconds between the 10 s windows the voices are found in
	MaxEmbS   = 5.0  // seconds of a voice per voice print
)

// Engine runs the speaker finder.
type Engine struct {
	Python    string // python3 with numpy and onnxruntime
	Dir       string // where the models are kept
	UserAgent string
	Threads   int

	mu        sync.Mutex
	checkedAt time.Time
	ok        bool
	why       string
	fetching  chan struct{}
	fetchErr  error
}

// Turn is a stretch one speaker talked; K is the speaker's number.
type Turn struct {
	S int64 `json:"s"`
	E int64 `json:"e"`
	K int   `json:"k"`
}

// Voice is one speaker of the recording.
type Voice struct {
	K      int       `json:"k"`
	MS     int64     `json:"ms"`     // how long they talked
	N      int       `json:"n"`      // how many voice prints made theirs
	Spread float64   `json:"spread"` // how different those prints were (0 = alike)
	Emb    []float32 `json:"emb"`    // their voice print, length 1
}

// Result is who spoke when.
type Result struct {
	Turns    []Turn  `json:"turns"`
	Speakers []Voice `json:"speakers"`
	Dim      int     `json:"dim"`
}

// Available reports whether this machine can run the speaker finder at all
// (python3 with numpy and onnxruntime), and if not, why.
func (e *Engine) Available() (bool, string) {
	if e == nil {
		return false, "not set up"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.checkedAt.IsZero() && time.Since(e.checkedAt) < 10*time.Minute {
		return e.ok, e.why
	}
	e.checkedAt = time.Now()
	py, err := exec.LookPath(e.Python)
	if err != nil {
		e.ok, e.why = false, "python3 is not installed here (the Docker image has it on Intel/AMD and ARM64)"
		return e.ok, e.why
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, py, "-c", "import numpy, onnxruntime").CombinedOutput()
	if err != nil {
		e.ok, e.why = false, "numpy and onnxruntime are missing for python3: "+lastLine(string(out), err.Error())
		return e.ok, e.why
	}
	e.ok, e.why = true, ""
	return true, ""
}

func (e *Engine) path(m Model) string { return filepath.Join(e.Dir, m.File) }

// Ready reports whether the models are downloaded.
func (e *Engine) Ready() bool {
	for _, m := range Models {
		if fi, err := os.Stat(e.path(m)); err != nil || fi.Size() < 1<<20 {
			return false
		}
	}
	return true
}

// Fetch downloads the models that are missing (once, however many ask).
func (e *Engine) Fetch(ctx context.Context) error {
	if e.Ready() {
		return nil
	}
	e.mu.Lock()
	ch := e.fetching
	if ch == nil {
		ch = make(chan struct{})
		e.fetching = ch
		go func() {
			err := e.fetchAll()
			e.mu.Lock()
			e.fetchErr, e.fetching = err, nil
			e.mu.Unlock()
			close(ch)
		}()
	}
	e.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fetchErr != nil {
		return fmt.Errorf("the speaker models could not be downloaded: %w", e.fetchErr)
	}
	return nil
}

func (e *Engine) fetchAll() error {
	if err := os.MkdirAll(e.Dir, 0o755); err != nil {
		return err
	}
	for _, m := range Models {
		if fi, err := os.Stat(e.path(m)); err == nil && fi.Size() >= 1<<20 {
			continue
		}
		if err := e.fetchOne(m); err != nil {
			return fmt.Errorf("%s: %w", m.File, err)
		}
	}
	return nil
}

func (e *Engine) fetchOne(m Model) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", e.UserAgent)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	part := e.path(m) + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), res.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && m.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		err = errors.New("the download is not the expected file")
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	return os.Rename(part, e.path(m))
}

// scriptPath writes the embedded program next to the models (once per version).
func (e *Engine) scriptPath() (string, error) {
	sum := sha256.Sum256(script)
	p := filepath.Join(e.Dir, "speakers-"+hex.EncodeToString(sum[:4])+".py")
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	if err := os.MkdirAll(e.Dir, 0o755); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, script, 0o644); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

// Options for one run.
type Options struct {
	Background  bool    // run at the lowest CPU priority
	MinSpeakerS float64 // voices with less speech than this are folded into the nearest
	Threshold   float64 // 0 = Threshold
}

// Diarize finds who spoke when in a 16 kHz mono WAV.
func (e *Engine) Diarize(ctx context.Context, wav string, o Options) (*Result, error) {
	if ok, why := e.Available(); !ok {
		return nil, errors.New(why)
	}
	if err := e.Fetch(ctx); err != nil {
		return nil, err
	}
	th := o.Threshold
	if th == 0 {
		th = Threshold
	}
	req := map[string]any{"cmd": "diarize", "wav": wav, "threshold": th, "step_s": StepS, "max_emb_s": MaxEmbS,
		"min_speaker_s": math.Max(o.MinSpeakerS, 1), "threads": max(1, e.Threads),
		"seg_model": e.path(Models[0]), "emb_model": e.path(Models[1])}
	var out Result
	if err := e.run(ctx, o.Background, req, &out); err != nil {
		return nil, err
	}
	for i := range out.Speakers {
		normalize(out.Speakers[i].Emb)
	}
	return &out, nil
}

func (e *Engine) run(ctx context.Context, background bool, req any, out any) error {
	sp, err := e.scriptPath()
	if err != nil {
		return err
	}
	py, err := exec.LookPath(e.Python)
	if err != nil {
		return err
	}
	bin, args := py, []string{sp}
	if background {
		if nice, err := exec.LookPath("nice"); err == nil {
			bin, args = nice, append([]string{"-n", "19", py}, args...)
		}
	}
	in, _ := json.Marshal(req)
	cmd := exec.CommandContext(ctx, bin, args...)
	// the threads asked for, and no more: numpy's BLAS would start one per core
	n := strconv.Itoa(max(1, e.Threads))
	cmd.Env = append(os.Environ(), "OPENBLAS_NUM_THREADS=1", "OMP_NUM_THREADS="+n, "MKL_NUM_THREADS=1", "NUMEXPR_NUM_THREADS=1")
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("finding who spoke failed: %s", lastLine(stderr.String(), err.Error()))
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("finding who spoke: unreadable answer: %w", err)
	}
	return nil
}

// Similarity is the cosine similarity of two voice prints.
func Similarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// Mean is the average direction of voice prints, weighted, length 1.
func Mean(embs [][]float32, weights []float64) []float32 {
	if len(embs) == 0 {
		return nil
	}
	sum := make([]float64, len(embs[0]))
	for i, e := range embs {
		if len(e) != len(sum) {
			continue
		}
		w := 1.0
		if i < len(weights) {
			w = weights[i]
		}
		for j, v := range e {
			sum[j] += w * float64(v)
		}
	}
	out := make([]float32, len(sum))
	for j, v := range sum {
		out[j] = float32(v)
	}
	normalize(out)
	return out
}

func normalize(v []float32) {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n = math.Sqrt(n); n > 0 {
		for i := range v {
			v[i] = float32(float64(v[i]) / n)
		}
	}
}

func lastLine(s, def string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return def
}
