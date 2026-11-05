package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRemoteModel is OpenAI's transcription model unless the person names another.
const DefaultRemoteModel = "gpt-4o-mini-transcribe"

// MaxRemoteBytes is OpenAI's upload limit.
const MaxRemoteBytes = 25 << 20

// Remote is an OpenAI-compatible /audio/transcriptions API: OpenAI itself
// with the person's own key, or a speech server on the network (speaches,
// LocalAI) at its base URL.
type Remote struct {
	BaseURL string
	Key     string
	Model   string
	HTTP    *http.Client
}

// NewRemote makes a client ("" base = OpenAI).
func NewRemote(baseURL, key, model string) *Remote {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = DefaultRemoteModel
	}
	return &Remote{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, Model: model, HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

// Transcribe sends the file and reads the words (with timings when the
// model gives them: the whisper models do, gpt-4o-*-transcribe does not).
func (c *Remote) Transcribe(ctx context.Context, path, lang, prompt string) (*Result, error) {
	if c.Key == "" && strings.Contains(c.BaseURL, "api.openai.com") {
		return nil, errors.New("add your OpenAI API key under Settings → AI")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > MaxRemoteBytes {
		return nil, fmt.Errorf("the recording is %d MB; OpenAI takes up to 25 MB", fi.Size()>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	verbose := strings.Contains(strings.ToLower(c.Model), "whisper")
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, err
	}
	_ = mw.WriteField("model", c.Model)
	if lang != "" {
		_ = mw.WriteField("language", lang)
	}
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	if verbose {
		_ = mw.WriteField("response_format", "verbose_json")
		_ = mw.WriteField("timestamp_granularities[]", "segment")
	} else {
		_ = mw.WriteField("response_format", "json")
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error.Message != "" {
			return nil, fmt.Errorf("transcription API: %s", e.Error.Message)
		}
		return nil, fmt.Errorf("transcription API: HTTP %d", res.StatusCode)
	}
	var out struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("transcription API: %w", err)
	}
	r := &Result{Language: LanguageCode(out.Language), Engine: "api " + c.Model, Segments: []Segment{}}
	for _, s := range out.Segments {
		if t := strings.TrimSpace(s.Text); t != "" && !isNoise(t) {
			r.Segments = append(r.Segments, Segment{Start: int64(s.Start * 1000), End: int64(s.End * 1000), Text: t})
		}
	}
	if len(r.Segments) > 0 {
		r.Text = JoinSegments(r.Segments)
	} else {
		r.Text = strings.TrimSpace(out.Text)
		if r.Text != "" {
			r.Segments = append(r.Segments, Segment{Start: 0, End: int64(out.Duration * 1000), Text: r.Text})
		}
	}
	return r, nil
}

// LanguageCode turns "english" (verbose_json says names) into "en".
func LanguageCode(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if len(v) <= 3 {
		return v
	}
	for _, l := range Languages {
		if strings.ToLower(l.Name) == v {
			return l.Code
		}
	}
	return v
}
