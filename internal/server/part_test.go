package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

func TestPartOfAMeeting(t *testing.T) {
	var asked string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &in)
		asked = in.Messages[len(in.Messages)-1].Content
		content := `{"title":"Shipping day","summary":"You asked about Friday; they agreed.","points":["- Ship on Friday","",""]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer fake.Close()

	a := newApp(t, nil)
	ctx := context.Background()
	m := &store.Memo{Title: "Release call", RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), DurationMS: 60000, Status: "done", Meeting: true,
		Transcript: "You: Hello.\nOthers: Hi.",
		Segments: []store.Segment{
			{Start: 0, End: 4000, Text: "Hello there.", Speaker: "You"},
			{Start: 10000, End: 15000, Text: "Shall we ship on Friday?", Speaker: "You"},
			{Start: 15000, End: 19000, Text: "Friday works.", Speaker: "Others"},
			{Start: 19000, End: 21000, Text: "Agreed.", Speaker: "Others"},
			{Start: 50000, End: 55000, Text: "Bye now.", Speaker: "Others"},
		}}
	if err := a.st.CreateMemo(ctx, a.ids["Alice"], m); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/memos/" + itoa(m.ID) + "/part"

	a.expect(http.StatusBadRequest, a.alice, "POST", path, map[string]any{"from": 9000, "to": 9000})
	a.expect(http.StatusBadRequest, a.alice, "POST", path, map[string]any{"from": 30000, "to": 45000}) // nothing said
	a.expect(http.StatusNotFound, a.bob, "POST", path, map[string]any{"from": 9000, "to": 22000})
	a.expect(http.StatusConflict, a.alice, "POST", path, map[string]any{"from": 9000, "to": 22000}) // no AI yet

	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "openai_api_key": "sk-test", "openai_base_url": fake.URL, "openai_model": "m"})
	out := a.ok(a.alice, "POST", path, map[string]any{"from": 9000, "to": 22000})
	if out["title"] != "Shipping day" || !strings.Contains(out["summary"].(string), "Friday") {
		t.Fatalf("part: %v", out)
	}
	if pts, _ := out["points"].([]any); len(pts) != 1 || pts[0] != "Ship on Friday" {
		t.Fatalf("points: %v", out["points"])
	}
	// only the picked stretch goes to the AI, turn by turn with the time
	if !strings.Contains(asked, "[0:10] You: Shall we ship on Friday?\n[0:15] Others: Friday works. Agreed.") || strings.Contains(asked, "Hello there") || strings.Contains(asked, "Bye now") {
		t.Fatalf("asked: %q", asked)
	}
}

func TestPartText(t *testing.T) {
	segs := []store.Segment{{Start: 0, End: 1000, Text: "One."}, {Start: 1000, End: 2000, Text: " Two. "}, {Start: 65000, End: 66000, Text: "Three."}}
	if got := partText(partSegments(segs, 500, 70000)); got != "[0:00] One. Two. Three." {
		t.Fatalf("partText: %q", got)
	}
	if got := partSegments(segs, 2000, 65000); len(got) != 0 {
		t.Fatalf("touching edges are outside: %v", got)
	}
}

func TestEchoIsDropped(t *testing.T) {
	segs := []speech.Segment{
		{Start: 0, End: 3000, Text: "Shall we ship on Friday?", Speaker: "You"},
		{Start: 3000, End: 6000, Text: "Friday works for us, Amra.", Speaker: "Others"},
		{Start: 3400, End: 6500, Text: "friday works for us amra", Speaker: "You"},                         // the mic heard the call
		{Start: 7000, End: 9000, Text: "Great, then Friday it is, and I write the notes.", Speaker: "You"}, // their own words
		{Start: 60000, End: 62000, Text: "Friday works", Speaker: "You"},                                   // long after: not an echo
	}
	got := dropEcho(segs)
	var you []string
	for _, sg := range got {
		if sg.Speaker == "You" {
			you = append(you, sg.Text)
		}
	}
	if len(got) != 4 || strings.Join(you, "|") != "Shall we ship on Friday?|Great, then Friday it is, and I write the notes.|Friday works" {
		t.Fatalf("echo: %v", got)
	}
	// only one speaker: nothing to compare with
	if len(dropEcho(segs[:1])) != 1 {
		t.Fatal("one speaker")
	}
}

func TestEchoIsCleanedFromOldMeetings(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	mk := func(edited bool) *store.Memo {
		m := &store.Memo{Title: "Call", RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), DurationMS: 10000, Status: "done", Meeting: true, Edited: edited,
			Transcript: "x", Segments: []store.Segment{
				{Start: 0, End: 3000, Text: "Hello all.", Speaker: "You"},
				{Start: 3000, End: 6000, Text: "Welcome back, everyone.", Speaker: "Others"},
				{Start: 3200, End: 6200, Text: "welcome back everyone", Speaker: "You"},
			}}
		if err := a.st.CreateMemo(ctx, a.ids["Alice"], m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m, kept := mk(false), mk(true)
	a.s.cleanEcho(ctx)
	got, _ := a.st.GetMemo(ctx, a.ids["Alice"], m.ID)
	if len(got.Segments) != 2 || got.Transcript != "You: Hello all.\nOthers: Welcome back, everyone." {
		t.Fatalf("cleaned: %v %q", got.Segments, got.Transcript)
	}
	if k, _ := a.st.GetMemo(ctx, a.ids["Alice"], kept.ID); len(k.Segments) != 3 {
		t.Fatalf("a corrected meeting was changed: %v", k.Segments)
	}
}
