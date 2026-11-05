package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/config"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// testWAV is two seconds of 16 kHz mono tone.
func testWAV() []byte {
	const rate = 16000
	frames := rate * 2
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+frames*2))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(frames*2))
	for i := 0; i < frames; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*220*float64(i)/rate) * float64(i) / float64(frames))
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	return b.Bytes()
}

// fakeTools puts a fake ffmpeg (copies a WAV to its last argument) and a
// fake whisper-cli (writes whisper's JSON with the words in $WORDS) on disk.
func fakeTools(t *testing.T, words, lang string) func(*config.Config) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "tone.wav")
	if err := os.WriteFile(wav, testWAV(), 0o644); err != nil {
		t.Fatal(err)
	}
	ff := filepath.Join(dir, "ffmpeg")
	_ = os.WriteFile(ff, []byte("#!/bin/sh\nfor a; do last=$a; done\ncp "+wav+" \"$last\"\n"), 0o755)
	js, _ := json.Marshal(map[string]any{"result": map[string]any{"language": lang}, "transcription": []any{
		map[string]any{"offsets": map[string]any{"from": 0, "to": 900}, "text": " " + strings.SplitN(words, "|", 2)[0]},
		map[string]any{"offsets": map[string]any{"from": 1000, "to": 2000}, "text": " " + strings.SplitN(words+"|", "|", 3)[1]},
		map[string]any{"offsets": map[string]any{"from": 2000, "to": 2000}, "text": " [BLANK_AUDIO]"},
	}})
	_ = os.WriteFile(filepath.Join(dir, "out.json"), js, 0o644)
	wh := filepath.Join(dir, "whisper-cli")
	_ = os.WriteFile(wh, []byte("#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -of ]; then of=$2; fi; shift; done\necho 'whisper_print_progress_callback: progress =  50%' >&2\ncp "+filepath.Join(dir, "out.json")+" \"$of.json\"\n"), 0o755)
	return func(c *config.Config) {
		c.FFmpegBin, c.WhisperBin = ff, wh
		_ = os.MkdirAll(c.ModelsDir, 0o755)
		m, _ := speech.ModelByID("")
		_ = os.WriteFile(filepath.Join(c.ModelsDir, m.File), append([]byte("lmgg"), make([]byte, 2<<20)...), 0o644)
	}
}

func (a *app) upload(auth string, fields map[string]string, name string, data []byte) (int, map[string]any) {
	a.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", a.url+"/api/v1/memos", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return res.StatusCode, m
}

func (a *app) waitDone(auth, memo string) map[string]any {
	a.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		m := a.ok(auth, "GET", "/api/v1/memos/"+memo, nil)
		if s := m["status"]; s == "done" || s == "failed" {
			return m
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.t.Fatalf("memo %s was not written out in time", memo)
	return nil
}

func TestMemoIsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	code, m := a.upload(a.alice, map[string]string{"title": "Groceries"}, "note.webm", []byte("not really audio"))
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, m)
	}
	mid := id(m)
	if m["title_set"] != true || m["has_audio"] != true {
		t.Fatalf("memo: %v", m)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/api/v1/memos/" + mid}, {"PUT", "/api/v1/memos/" + mid}, {"DELETE", "/api/v1/memos/" + mid},
		{"GET", "/api/v1/memos/" + mid + "/audio"}, {"POST", "/api/v1/memos/" + mid + "/retry"}, {"POST", "/api/v1/memos/" + mid + "/send"}} {
		a.expect(http.StatusNotFound, a.bob, c.method, c.path, map[string]any{"title": "mine now", "to": "note"})
	}
	if l := a.ok(a.bob, "GET", "/api/v1/memos", nil); num(l, "total") != 0 {
		t.Fatalf("Bob sees Alice's memos: %v", l)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/memos", nil); num(l, "total") != 1 {
		t.Fatalf("Alice's list: %v", l)
	}
	// the audio comes back to Alice
	req, _ := http.NewRequest("GET", a.url+"/api/v1/memos/"+mid+"/audio", nil)
	req.Header.Set("Authorization", "Bearer "+a.alice)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "not really audio" {
		t.Fatalf("audio: %q", b)
	}
	a.ok(a.alice, "DELETE", "/api/v1/memos/"+mid, nil)
	if files, _ := os.ReadDir(a.s.audioDir(a.ids["Alice"])); len(files) != 0 {
		t.Fatalf("the recording stayed after delete: %v", files)
	}
}

func TestRecordingIsWrittenOut(t *testing.T) {
	a := newApp(t, fakeTools(t, "Пробудио сам се рано.|Купи хљеб и млијеко.", "sr"))
	code, m := a.upload(a.alice, map[string]string{"recorded_at": "2026-11-05T08:30:00+01:00"}, "morning.webm", []byte("webm bytes"))
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, m)
	}
	done := a.waitDone(a.alice, id(m))
	if done["status"] != "done" {
		t.Fatalf("not written out: %v", done)
	}
	if done["transcript"] != "Probudio sam se rano. Kupi hljeb i mlijeko." {
		t.Fatalf("transcript (Latin letters, no blank audio): %q", done["transcript"])
	}
	if done["language"] != "sr" || !strings.HasPrefix(done["engine"].(string), "whisper ") {
		t.Fatalf("language/engine: %v %v", done["language"], done["engine"])
	}
	if done["title"] != "Probudio sam se rano" {
		t.Fatalf("title from the first words: %q", done["title"])
	}
	if d := num(done, "duration_ms"); d < 1900 || d > 2100 {
		t.Fatalf("duration from the WAV: %v", d)
	}
	if p, _ := done["peaks"].([]any); len(p) < 100 {
		t.Fatalf("peaks: %d", len(p))
	}
	if segs, _ := done["segments"].([]any); len(segs) != 2 {
		t.Fatalf("segments: %v", done["segments"])
	}
	if done["audio_type"] != "audio/mp4" {
		t.Fatalf("a WebM recording gets a copy every browser plays: %v", done["audio_type"])
	}
	// search finds it in Latin letters without the accents
	if l := a.ok(a.alice, "GET", "/api/v1/search?q=hljeb", nil); num(l, "total") != 1 {
		t.Fatalf("search: %v", l)
	}
	// dictation for other apps: words back, nothing kept
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "clip.m4a")
	_, _ = fw.Write([]byte("m4a"))
	_ = mw.Close()
	req, _ := http.NewRequest("POST", a.url+"/api/v1/transcribe", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+a.alice)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var tr map[string]any
	_ = json.NewDecoder(res.Body).Decode(&tr)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(tr["text"].(string), "Probudio") {
		t.Fatalf("transcribe: %d %v", res.StatusCode, tr)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/memos", nil); num(l, "total") != 1 {
		t.Fatalf("dictation kept a memo: %v", l)
	}
}

func TestNoWhisperFailsPlainly(t *testing.T) {
	a := newApp(t, nil)
	_, m := a.upload(a.alice, nil, "x.wav", testWAV())
	done := a.waitDone(a.alice, id(m))
	if done["status"] != "failed" || !strings.Contains(done["error"].(string), "whisper.cpp is not installed") {
		t.Fatalf("want a plain failure: %v", done)
	}
}

func TestTextMemoEditAndSearch(t *testing.T) {
	a := newApp(t, nil)
	m := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "Ćevapi u Želji, deset u pola. Then the cinema.", "kind": "journal"})
	mid := id(m)
	if m["status"] != "done" || m["kind"] != "journal" || m["kind_set"] != true || m["title"] == "" {
		t.Fatalf("typed memo: %v", m)
	}
	a.expect(http.StatusBadRequest, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "x", "kind": "poem"})
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/memos/"+mid, map[string]any{"kind": "poem"})
	u := a.ok(a.alice, "PUT", "/api/v1/memos/"+mid, map[string]any{"title": "Dinner", "starred": true, "filed": true, "tags": "#Food, food , Sarajevo"})
	if u["title"] != "Dinner" || u["title_set"] != true || u["starred"] != true || u["filed"] != true || u["tags"] != "food, sarajevo" {
		t.Fatalf("update: %v", u)
	}
	for _, q := range []string{"cevapi", "ZELJI", "dinner cinema"} {
		if l := a.ok(a.alice, "GET", "/api/v1/memos?q="+strings.ReplaceAll(q, " ", "+"), nil); num(l, "total") != 1 {
			t.Fatalf("search %q: %v", q, l)
		}
	}
	if l := a.ok(a.alice, "GET", "/api/v1/memos?inbox=1", nil); num(l, "total") != 0 {
		t.Fatalf("a filed memo left the inbox: %v", l)
	}
	if l := a.ok(a.bob, "GET", "/api/v1/search?q=cevapi", nil); num(l, "total") != 0 {
		t.Fatalf("Bob finds Alice's memo: %v", l)
	}
	day := a.ok(a.alice, "GET", "/api/v1/day/"+store.Today(), nil)
	if num(day, "count") != 1 {
		t.Fatalf("day: %v", day)
	}
	sum := a.ok(a.alice, "GET", "/api/v1/summary", nil)
	if num(sum, "memos") != 1 || num(sum, "today") != 1 || num(sum, "starred") != 1 {
		t.Fatalf("summary: %v", sum)
	}
}

func TestSettingsArePersonal(t *testing.T) {
	a := newApp(t, nil)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"language": "bs", "vocabulary": "Amra, Kibe Mahala", "engine": "api", "speech_model": "small"})
	al := a.ok(a.alice, "GET", "/api/v1/settings", nil)
	if al["language"] != "bs" || al["engine"] != "api" || al["speech_model"] != "small" {
		t.Fatalf("Alice: %v", al)
	}
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"speech_model": "base"})
	bo := a.ok(a.bob, "GET", "/api/v1/settings", nil)
	if bo["language"] != "" || bo["engine"] != "local" || bo["vocabulary"] != "" {
		t.Fatalf("Bob got Alice's settings: %v", bo)
	}
	if bo["speech_model"] != "small" {
		t.Fatalf("a member changed the box's speech model: %v", bo["speech_model"])
	}
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/settings", map[string]any{"engine": "cloud"})
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/settings", map[string]any{"language": "english"})
}

func TestExportImportIsIdempotent(t *testing.T) {
	a := newApp(t, nil)
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "First memo."})
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "Second memo.", "kind": "idea"})
	req, _ := http.NewRequest("GET", a.url+"/api/v1/export.json", nil)
	req.Header.Set("Authorization", "Bearer "+a.alice)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", a.url+"/api/v1/import", bytes.NewReader(doc))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+a.bob)
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("import: %v %v", err, res.StatusCode)
		}
		res.Body.Close()
	}
	if l := a.ok(a.bob, "GET", "/api/v1/memos", nil); num(l, "total") != 2 {
		t.Fatalf("Bob after two imports: %v", l)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/memos", nil); num(l, "total") != 2 {
		t.Fatalf("Alice changed: %v", l)
	}
}

func TestLinkScopes(t *testing.T) {
	req := func(method, path string) *http.Request { return httptest.NewRequest(method, path, nil) }
	cases := []struct {
		scopes []string
		method string
		path   string
		want   bool
	}{
		{[]string{"voice:read"}, "GET", "/api/v1/memos", true},
		{[]string{"voice:read"}, "GET", "/api/v1/memos/12/audio", true},
		{[]string{"voice:read"}, "GET", "/api/v1/day/2026-11-05", true},
		{[]string{"voice:read"}, "POST", "/api/v1/memos", false},
		{[]string{"voice:write"}, "POST", "/api/v1/memos", true},
		{[]string{"voice:write"}, "DELETE", "/api/v1/memos/3", true},
		{[]string{"voice:write"}, "POST", "/api/v1/memos/3/send", false},
		{[]string{"voice:transcribe"}, "POST", "/api/v1/transcribe", true},
		{[]string{"voice:transcribe"}, "GET", "/api/v1/memos", false},
		{[]string{"voice:read", "voice:write"}, "PUT", "/api/v1/settings", false},
		{nil, "GET", "/api/v1/auth/me", true},
	}
	for _, c := range cases {
		if got := linkAllows(c.scopes, req(c.method, c.path)); got != c.want {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.method, c.path, got, c.want)
		}
	}
}

func TestApplyTidy(t *testing.T) {
	rec := time.Date(2026, 11, 5, 9, 0, 0, 0, time.Local)
	m := &store.Memo{Title: "My own name", TitleSet: true, Kind: "", Actions: []store.Action{{Type: "task", Title: "Sent before", SentTo: "tasks", SentRef: "7"}}}
	applyTidy(m, tidyOut{Title: "AI title", Summary: " Short. ", Text: "Clean text.", Kind: "TODO", Tags: []string{"a", "b", "c", "d"}, Actions: []store.Action{
		{Type: "task", Title: "Call the dentist", Due: "2026-11-06"},
		{Type: "task", Title: "Sent before"},
		{Type: "task", Title: "  "},
		{Type: "event", Title: "Meeting", Start: "2026-11-14T17:00", End: "2026-11-14T16:00"},
		{Type: "event", Title: "No date", Start: "next week"},
		{Type: "expense", Title: "Lunch", Amount: 12.345, Currency: "bam"},
		{Type: "expense", Title: "Free", Amount: 0},
		{Type: "journal", Text: "A good day."},
		{Type: "poem", Title: "?"},
	}}, rec)
	if m.Title != "My own name" || m.Summary != "Short." || m.Tidy != "Clean text." || m.Kind != "todo" || m.Tags != "a, b, c" {
		t.Fatalf("fields: %+v", m)
	}
	var types []string
	for _, a := range m.Actions {
		types = append(types, a.Type+":"+a.Title)
	}
	want := "task:Sent before task:Call the dentist event:Meeting expense:Lunch journal:"
	if strings.Join(types, " ") != want {
		t.Fatalf("actions: %v", types)
	}
	if ev := m.Actions[2]; ev.End != "" {
		t.Fatalf("an end before the start is dropped: %+v", ev)
	}
	if ex := m.Actions[3]; ex.Amount != 12.35 || ex.Currency != "BAM" || ex.Date != "2026-11-05" {
		t.Fatalf("expense: %+v", ex)
	}
	if m.Actions[0].SentTo != "tasks" {
		t.Fatalf("a sent suggestion is kept as sent")
	}
}

func TestLatinLetters(t *testing.T) {
	for in, want := range map[string]string{"Љубав и Њива": "Ljubav i Njiva", "ЏЕП": "DŽEP", "Ђорђе ће доћи": "Đorđe će doći", "already latin": "already latin"} {
		if got := speech.ToLatin(in); got != want {
			t.Errorf("ToLatin(%q) = %q, want %q", in, got, want)
		}
	}
	if speech.Fold("Ćevapi u ŽELJI") != "cevapi u zelji" {
		t.Errorf("fold: %q", speech.Fold("Ćevapi u ŽELJI"))
	}
}
