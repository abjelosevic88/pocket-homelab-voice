package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/config"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

func item(m map[string]any) map[string]any { v, _ := m["item"].(map[string]any); return v }

func TestDictionary(t *testing.T) {
	a := newApp(t, nil)
	add := func(auth string, want int, body map[string]any) map[string]any {
		return item(a.expect(want, auth, "POST", "/api/v1/dictionary", body))
	}
	pa := add(a.alice, http.StatusCreated, map[string]any{"phrase": "pocket ai", "replace_with": "Pocket AI"})
	if pa["phrase"] != "pocket ai" || pa["replace_with"] != "Pocket AI" || pa["source"] != "manual" || num(pa, "uses") != 0 || pa["created_at"] == "" {
		t.Fatalf("added: %v", pa)
	}
	for _, k := range []string{"id", "phrase", "replace_with", "source", "uses", "created_at"} {
		if _, ok := pa[k]; !ok {
			t.Fatalf("item has no %q: %v", k, pa)
		}
	}
	// the same phrase in other capitals: the row that is there, with the new replacement
	again := add(a.alice, http.StatusOK, map[string]any{"phrase": "Pocket  AI", "replace_with": "PocketAI"})
	if id(again) != id(pa) || again["replace_with"] != "PocketAI" || again["phrase"] != "pocket ai" {
		t.Fatalf("again: %v", again)
	}
	// without replace_with it stays as it is
	if x := add(a.alice, http.StatusOK, map[string]any{"phrase": "POCKET AI"}); x["replace_with"] != "PocketAI" {
		t.Fatalf("kept: %v", x)
	}
	amra := add(a.alice, http.StatusCreated, map[string]any{"phrase": "Amra", "source": "learned"})
	if amra["source"] != "learned" || amra["replace_with"] != "" {
		t.Fatalf("learned word: %v", amra)
	}
	for _, bad := range []map[string]any{{"phrase": " "}, {"phrase": strings.Repeat("x", 101)}, {"phrase": "ok", "replace_with": strings.Repeat("y", 201)}, {"phrase": "ok", "source": "robot"}} {
		a.expect(http.StatusBadRequest, a.alice, "POST", "/api/v1/dictionary", bad)
	}
	l := a.ok(a.alice, "GET", "/api/v1/dictionary", nil)
	items, _ := l["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["phrase"] != "Amra" || items[1].(map[string]any)["phrase"] != "pocket ai" {
		t.Fatalf("list by phrase: %v", l)
	}
	up := item(a.ok(a.alice, "PUT", "/api/v1/dictionary/"+id(pa), map[string]any{"replace_with": "Pocket AI"}))
	if up["replace_with"] != "Pocket AI" || up["phrase"] != "pocket ai" {
		t.Fatalf("put: %v", up)
	}
	a.expect(http.StatusConflict, a.alice, "PUT", "/api/v1/dictionary/"+id(pa), map[string]any{"phrase": "amra"})
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/dictionary/"+id(pa), map[string]any{"phrase": ""})
	// Bob has his own
	a.expect(http.StatusNotFound, a.bob, "PUT", "/api/v1/dictionary/"+id(pa), map[string]any{"replace_with": "x"})
	a.expect(http.StatusNotFound, a.bob, "DELETE", "/api/v1/dictionary/"+id(pa), nil)
	if l := a.ok(a.bob, "GET", "/api/v1/dictionary", nil); len(l["items"].([]any)) != 0 {
		t.Fatalf("Bob sees Alice's words: %v", l)
	}
	add(a.bob, http.StatusCreated, map[string]any{"phrase": "pocket ai"})
	a.expect(http.StatusNoContent, a.alice, "DELETE", "/api/v1/dictionary/"+id(amra), nil)
	a.expect(http.StatusNotFound, a.alice, "DELETE", "/api/v1/dictionary/"+id(amra), nil)
}

func TestReplacer(t *testing.T) {
	rp := newReplacer([]store.Word{
		{ID: 1, Phrase: "pocket ai", ReplaceWith: "Pocket AI"},
		{ID: 2, Phrase: "cevapi", ReplaceWith: "ćevapi"},
		{ID: 3, Phrase: "Amra"}, // a word to expect: nothing to replace
		{ID: 4, Phrase: "k8s", ReplaceWith: "Kubernetes"},
	})
	uses := map[int64]int{}
	got := rp.apply("i asked pocket ai, then POCKET\nAI again; pocketaid and mypocket ai stay. Cevapi! k8s.", uses)
	want := "i asked Pocket AI, then Pocket AI again; pocketaid and mypocket ai stay. ćevapi! Kubernetes."
	if got != want {
		t.Fatalf("replaced:\n got %q\nwant %q", got, want)
	}
	if uses[1] != 2 || uses[2] != 1 || uses[4] != 1 || uses[3] != 0 {
		t.Fatalf("uses: %v", uses)
	}
	if again := rp.apply(want, uses); again != want || uses[1] != 2 {
		t.Fatalf("a second pass changes nothing: %q %v", again, uses)
	}
	if p := speechPrompt("Kibe Mahala, Amra", []store.Word{{Phrase: "amra"}, {Phrase: "pocket ai", ReplaceWith: "Pocket AI"}, {Phrase: "Lejla"}}); p != "Kibe Mahala, Amra, Pocket AI, Lejla" {
		t.Fatalf("prompt: %q", p)
	}
}

func TestDictionaryInWritingOut(t *testing.T) {
	a := newApp(t, fakeTools(t, "Ask pocket ai about it.|Then call amra.", "en"))
	a.ok(a.alice, "POST", "/api/v1/dictionary", map[string]any{"phrase": "pocket ai", "replace_with": "Pocket AI"})
	a.ok(a.alice, "POST", "/api/v1/dictionary", map[string]any{"phrase": "amra", "replace_with": "Amra"})
	_, m := a.upload(a.alice, nil, "note.webm", []byte("webm"))
	done := a.waitDone(a.alice, id(m))
	if done["transcript"] != "Ask Pocket AI about it. Then call Amra." {
		t.Fatalf("memo transcript: %q", done["transcript"])
	}
	// dictation too
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
	if res.StatusCode != 200 || tr["text"] != "Ask Pocket AI about it. Then call Amra." {
		t.Fatalf("transcribe: %d %v", res.StatusCode, tr)
	}
	for _, x := range a.ok(a.alice, "GET", "/api/v1/dictionary", nil)["items"].([]any) {
		if num(x.(map[string]any), "uses") != 2 {
			t.Fatalf("uses after a memo and a dictation: %v", x)
		}
	}
}

func TestDictationStyles(t *testing.T) {
	a := newApp(t, nil)
	st := a.ok(a.alice, "GET", "/api/v1/dictation/styles", nil)
	if st["default"] != "natural" || len(st["apps"].([]any)) != 0 {
		t.Fatalf("default styles: %v", st)
	}
	put := map[string]any{"default": "formal", "apps": []any{
		map[string]any{"app_id": "com.tinyspeck.slackmacgap", "app": "Slack", "style": "casual"},
		map[string]any{"app_id": "", "app": "Terminal", "style": "code"},
		map[string]any{"app_id": "com.apple.Terminal", "app": "Terminal", "style": "none"},
		map[string]any{"app_id": "com.tinyspeck.slackmacgap", "app": "Slack", "style": "formal"}, // twice: the first counts
	}}
	got := a.ok(a.alice, "PUT", "/api/v1/dictation/styles", put)
	if got["default"] != "formal" || len(got["apps"].([]any)) != 3 {
		t.Fatalf("put: %v", got)
	}
	st = a.ok(a.alice, "GET", "/api/v1/dictation/styles", nil)
	if first := st["apps"].([]any)[0].(map[string]any); first["app_id"] != "com.tinyspeck.slackmacgap" || first["app"] != "Slack" || first["style"] != "casual" {
		t.Fatalf("stored: %v", st)
	}
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/dictation/styles", map[string]any{"default": "shouty", "apps": []any{}})
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/dictation/styles", map[string]any{"default": "natural", "apps": []any{map[string]any{"app": "Mail", "style": "loud"}}})
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/dictation/styles", map[string]any{"default": "natural", "apps": []any{map[string]any{"style": "casual"}}})
	if b := a.ok(a.bob, "GET", "/api/v1/dictation/styles", nil); b["default"] != "natural" || len(b["apps"].([]any)) != 0 {
		t.Fatalf("Bob has Alice's styles: %v", b)
	}

	// polishing: "none" needs no AI and makes only the dictionary's replacements
	a.ok(a.alice, "POST", "/api/v1/dictionary", map[string]any{"phrase": "git hub", "replace_with": "GitHub"})
	p := a.ok(a.alice, "POST", "/api/v1/polish", map[string]any{"text": " push it to git hub ", "style": "none"})
	if p["text"] != "push it to GitHub" || p["style"] != "none" {
		t.Fatalf("polish none: %v", p)
	}
	// the app's own style (by bundle id or by name)
	if p := a.ok(a.alice, "POST", "/api/v1/polish", map[string]any{"text": "ls dash la", "app": "Terminal", "app_id": "com.apple.Terminal"}); p["text"] != "ls dash la" || p["style"] != "none" {
		t.Fatalf("polish for Terminal: %v", p)
	}
	// other styles need an AI, as before
	a.expect(http.StatusUnprocessableEntity, a.alice, "POST", "/api/v1/polish", map[string]any{"text": "hey there", "app": "Slack"})
	a.expect(http.StatusBadRequest, a.alice, "POST", "/api/v1/polish", map[string]any{"text": "hey", "style": "pirate"})

	// rewriting needs an AI too
	m := a.expect(http.StatusConflict, a.alice, "POST", "/api/v1/rewrite", map[string]any{"text": "see you at 5", "instruction": "make it more formal"})
	if m["error"] == "" {
		t.Fatalf("rewrite without AI: %v", m)
	}
	a.expect(http.StatusBadRequest, a.alice, "POST", "/api/v1/rewrite", map[string]any{"text": "x", "instruction": " "})
	a.expect(http.StatusBadRequest, a.alice, "POST", "/api/v1/rewrite", map[string]any{"text": "x", "instruction": "y", "style": "pirate"})
}

func TestDictionaryLinkScope(t *testing.T) {
	for _, c := range []struct {
		scopes       []string
		method, path string
		want         bool
	}{
		{[]string{"voice:read"}, "GET", "/api/v1/dictionary", true},
		{[]string{"voice:read"}, "GET", "/api/v1/dictation/styles", true},
		{[]string{"voice:read", "voice:write", "voice:transcribe"}, "POST", "/api/v1/dictionary", false},
		{[]string{"voice:read", "voice:write", "voice:transcribe"}, "PUT", "/api/v1/dictionary/1", false},
		{[]string{"voice:read", "voice:write", "voice:transcribe"}, "DELETE", "/api/v1/dictionary/1", false},
		{[]string{"voice:read", "voice:write", "voice:transcribe"}, "PUT", "/api/v1/dictation/styles", false},
		{[]string{"voice:transcribe"}, "POST", "/api/v1/rewrite", true},
		{[]string{"voice:read"}, "POST", "/api/v1/rewrite", false},
	} {
		if got := linkAllows(c.scopes, httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("%v %s %s = %v", c.scopes, c.method, c.path, got)
		}
	}
}

// stereoWAV is two seconds of 16 kHz stereo: a tone on each channel that is on.
func stereoWAV(left, right bool) []byte {
	const rate = 16000
	frames := rate * 2
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+frames*4))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(2), uint32(rate), uint32(rate * 4), uint16(4), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(frames*4))
	for i := 0; i < frames; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*220*float64(i)/rate))
		for _, on := range []bool{left, right} {
			x := int16(0)
			if on {
				x = v
			}
			_ = binary.Write(&b, binary.LittleEndian, x)
		}
	}
	return b.Bytes()
}

// meetingTools is fakeTools with an ffmpeg that always makes this stereo recording.
func meetingTools(t *testing.T, left, right bool) func(*config.Config) {
	base := fakeTools(t, "Shall we ship on Friday?|Yes, Friday works.", "en")
	dir := t.TempDir()
	wav := filepath.Join(dir, "call.wav")
	if err := os.WriteFile(wav, stereoWAV(left, right), 0o644); err != nil {
		t.Fatal(err)
	}
	ff := filepath.Join(dir, "ffmpeg")
	_ = os.WriteFile(ff, []byte("#!/bin/sh\nfor a; do last=$a; done\ncp "+wav+" \"$last\"\n"), 0o755)
	// each channel says its own words, so neither is taken for the other's echo
	for name, said := range map[string][2]string{"you": {"Shall we ship on Friday?", "Then I write the notes."}, "others": {"Yes, Friday works.", "Good, we agree."}} {
		js, _ := json.Marshal(map[string]any{"result": map[string]any{"language": "en"}, "transcription": []any{
			map[string]any{"offsets": map[string]any{"from": 0, "to": 900}, "text": " " + said[0]},
			map[string]any{"offsets": map[string]any{"from": 1000, "to": 2000}, "text": " " + said[1]},
		}})
		_ = os.WriteFile(filepath.Join(dir, name+".json"), js, 0o644)
	}
	wh := filepath.Join(dir, "whisper-cli")
	_ = os.WriteFile(wh, []byte("#!/bin/sh\nwhile [ $# -gt 0 ]; do case $1 in -of) of=$2;; -f) f=$2;; esac; shift; done\n"+
		"case $f in *-others*) src=others;; *) src=you;; esac\ncp "+dir+"/$src.json \"$of.json\"\n"), 0o755)
	return func(c *config.Config) { base(c); c.FFmpegBin, c.WhisperBin = ff, wh }
}

func TestMeetingIsWrittenOutPerSpeaker(t *testing.T) {
	a := newApp(t, meetingTools(t, true, true))
	code, m := a.upload(a.alice, map[string]string{"meeting": "1", "title": "Release call"}, "call.m4a", []byte("m4a"))
	if code != http.StatusCreated || m["meeting"] != true || m["kind"] != "meeting" || m["kind_set"] != true {
		t.Fatalf("upload: %d %v", code, m)
	}
	done := a.waitDone(a.alice, id(m))
	if done["status"] != "done" {
		t.Fatalf("not written out: %v", done)
	}
	segs, _ := done["segments"].([]any)
	speakers := map[string]int{}
	for _, s := range segs {
		speakers[s.(map[string]any)["speaker"].(string)]++
	}
	if len(segs) != 4 || speakers["You"] != 2 || speakers["Others"] != 2 {
		t.Fatalf("segments: %v", segs)
	}
	tr := done["transcript"].(string)
	if !strings.HasPrefix(tr, "You: Shall we ship on Friday?") || !strings.Contains(tr, "\nOthers: ") || strings.Count(tr, "\n") < 1 {
		t.Fatalf("transcript: %q", tr)
	}
	if done["title"] != "Release call" {
		t.Fatalf("title: %v", done["title"])
	}

	// no sound from the computer: one speaker
	b := newApp(t, meetingTools(t, true, false))
	_, m2 := b.upload(b.alice, map[string]string{"meeting": "1"}, "call.m4a", []byte("m4a"))
	done2 := b.waitDone(b.alice, id(m2))
	segs2, _ := done2["segments"].([]any)
	for _, s := range segs2 {
		if s.(map[string]any)["speaker"] != "You" {
			t.Fatalf("one speaker: %v", segs2)
		}
	}
	if len(segs2) != 2 || strings.Contains(done2["transcript"].(string), "Others:") {
		t.Fatalf("one speaker: %v %q", segs2, done2["transcript"])
	}
	// an ordinary memo has no speakers
	c := newApp(t, fakeTools(t, "Just a note.|Nothing more.", "en"))
	_, m3 := c.upload(c.alice, nil, "n.webm", []byte("webm"))
	done3 := c.waitDone(c.alice, id(m3))
	for _, s := range done3["segments"].([]any) {
		if _, ok := s.(map[string]any)["speaker"]; ok {
			t.Fatalf("speaker on a memo: %v", s)
		}
	}
	if done3["meeting"] != false {
		t.Fatalf("meeting: %v", done3["meeting"])
	}
}
