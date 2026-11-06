package server

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/config"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/speakers"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// voice is a made-up voice print: mostly direction d, a little of e.
func voice(d, e int, mix float64) []float32 {
	v := make([]float32, 8)
	v[d] = float32(math.Sqrt(1 - mix*mix))
	v[e] += float32(mix)
	return v
}

func TestIdentify(t *testing.T) {
	m := &store.Memo{ID: 1}
	segs := []store.Segment{
		{Start: 0, End: 2000, Text: "Morning all.", Speaker: "You"},
		{Start: 2000, End: 4000, Text: "Hi, good morning.", Speaker: "Others"},
		{Start: 4000, End: 4500, Text: "Let's go.", Speaker: "Others"}, // over silence, again and again
		{Start: 5000, End: 5500, Text: "Let's go.", Speaker: "Others"},
		{Start: 6000, End: 6500, Text: "Let's go.", Speaker: "Others"},
		{Start: 7000, End: 9000, Text: "I start with the release.", Speaker: "Others"},
		{Start: 9000, End: 9500, Text: "Thanks.", Speaker: "Others"},
		{Start: 9500, End: 9900, Text: "Thanks.", Speaker: "Others"},
		{Start: 9900, End: 9950, Text: "Thanks.", Speaker: "Others"}, // the model looping
		{Start: 12500, End: 13000, Text: "Sure.", Speaker: "Others"}, // nobody heard: the nearest voice
	}
	res := &speakers.Result{
		Turns: []speakers.Turn{{S: 2000, E: 4000, K: 3}, {S: 7000, E: 10000, K: 1}, {S: 14000, E: 15000, K: 1}},
		Speakers: []speakers.Voice{
			{K: 1, MS: 4000, Emb: voice(1, 0, 0)},
			{K: 3, MS: 2000, Emb: voice(3, 0, 0)},
		},
	}
	out := identify(m, segs, res)
	var got []string
	for _, sg := range out {
		got = append(got, sg.Speaker+":"+sg.Text)
	}
	want := "You:Morning all.|S1:Hi, good morning.|S2:I start with the release.|S2:Thanks.|S2:Thanks.|S2:Sure."
	if strings.Join(got, "|") != want {
		t.Fatalf("segments:\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
	if len(m.Speakers) != 3 || m.Speakers[0].State != store.SpeakerYou || m.Speakers[1].Key != "S1" || m.Speakers[2].Key != "S2" {
		t.Fatalf("speakers: %+v", m.Speakers)
	}
	if m.Speakers[2].MS != 4000 || m.Speakers[2].Sample != [2]int64{7000, 10000} || len(m.SpeakerVoices["S1"]) != 8 {
		t.Fatalf("S2: %+v voices %v", m.Speakers[2], m.SpeakerVoices)
	}
	if tr := conversation(out, func(k string) string { return speakerLabel(m, k) }); !strings.HasPrefix(tr, "You: Morning all.\nSpeaker 1: Hi, good morning.\nSpeaker 2: I start") {
		t.Fatalf("transcript: %q", tr)
	}
}

func TestMatchPeople(t *testing.T) {
	people := []store.Person{
		{ID: 1, Name: "Alex", Voices: []store.Voice{{Emb: voice(1, 0, 0), MS: 60000}}},
		{ID: 2, Name: "Heidi", Voices: []store.Voice{{Emb: voice(2, 0, 0), MS: 60000}}},
		{ID: 3, Name: "Arno", Voices: []store.Voice{{Emb: voice(3, 0, 0), MS: 60000}}},
	}
	m := &store.Memo{ID: 9, Speakers: []store.Speaker{
		{Key: "You", State: store.SpeakerYou},
		{Key: "S1", State: store.SpeakerUnknown},                       // much like Alex
		{Key: "S2", State: store.SpeakerUnknown},                       // a bit like Heidi
		{Key: "S3", State: store.SpeakerUnknown},                       // like nobody
		{Key: "S4", State: store.SpeakerUnknown},                       // also much like Alex, less than S1
		{Key: "S5", State: store.SpeakerUnknown, Rejected: []int64{3}}, // like Arno, but "not Arno"
	}, SpeakerVoices: map[string][]float32{
		"S1": voice(1, 4, 0.3), "S2": voice(2, 4, 0.8), "S3": voice(5, 0, 0), "S4": voice(1, 4, 0.5), "S5": voice(3, 0, 0),
	}}
	learn := matchPeople(m, people)
	st := func(k string) store.Speaker { return *speakerOf(m, k) }
	if s := st("S1"); s.State != store.SpeakerAuto || s.Name != "Alex" || s.PersonID != 1 || s.Score < 0.9 {
		t.Fatalf("S1: %+v", s)
	}
	if s := st("S2"); s.State != store.SpeakerSuggested || s.Suggest != "Heidi" || s.Name != "" {
		t.Fatalf("S2: %+v", s)
	}
	if s := st("S3"); s.State != store.SpeakerUnknown || s.Name != "" || s.Suggest != "" {
		t.Fatalf("S3: %+v", s)
	}
	if s := st("S4"); s.State != store.SpeakerUnknown { // Alex is S1 already
		t.Fatalf("S4: %+v", s)
	}
	if s := st("S5"); s.State != store.SpeakerUnknown {
		t.Fatalf("S5 was said not to be Arno: %+v", s)
	}
	if len(learn) != 1 || learn[0] != 1 {
		t.Fatalf("learn: %v", learn)
	}
}

func TestRelabel(t *testing.T) {
	in := "Speaker 1: Hello.\nYou: Hi Speaker 1, and Speaker 12.\nSpeaker 1 agreed."
	want := "Alex: Hello.\nYou: Hi Alex, and Speaker 12.\nAlex agreed."
	if got := relabel(in, "Speaker 1", "Alex"); got != want {
		t.Fatalf("relabel: %q", got)
	}
}

// meeting makes a meeting with two voices on the call, written out already.
func meeting(t *testing.T, a *app, v1, v2 []float32) *store.Memo {
	t.Helper()
	m := &store.Memo{Title: "Stand-up", RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), DurationMS: 60000, Status: "done", Meeting: true,
		Tidy: "Speaker 1: We ship Friday.\nSpeaker 2: Agreed.", Summary: "Speaker 1 wants to ship on Friday.",
		Segments: []store.Segment{
			{Start: 0, End: 3000, Text: "Shall we ship?", Speaker: "You"},
			{Start: 3000, End: 6000, Text: "We ship Friday.", Speaker: "S1"},
			{Start: 6000, End: 8000, Text: "Agreed.", Speaker: "S2"},
			{Start: 8000, End: 9000, Text: "Me too.", Speaker: "S2"},
		},
		Speakers: []store.Speaker{
			{Key: "You", Name: "You", State: store.SpeakerYou, MS: 3000},
			{Key: "S1", State: store.SpeakerUnknown, MS: 3000, Sample: [2]int64{3000, 6000}},
			{Key: "S2", State: store.SpeakerUnknown, MS: 3000, Sample: [2]int64{6000, 9000}},
		},
		SpeakerVoices: map[string][]float32{"S1": v1, "S2": v2}}
	rebuildTranscript(m)
	if err := a.st.CreateMemo(context.Background(), a.ids["Alice"], m); err != nil {
		t.Fatal(err)
	}
	return m
}

func speakerIn(m map[string]any, key string) map[string]any {
	for _, x := range m["speakers"].([]any) {
		if sp := x.(map[string]any); sp["key"] == key {
			return sp
		}
	}
	return nil
}

func TestNamingVoices(t *testing.T) {
	a := newApp(t, nil)
	m := meeting(t, a, voice(1, 0, 0), voice(2, 0, 0))
	base := "/api/v1/memos/" + itoa(m.ID)
	if tr := m.Transcript; tr != "You: Shall we ship?\nSpeaker 1: We ship Friday.\nSpeaker 2: Agreed. Me too." {
		t.Fatalf("transcript: %q", tr)
	}
	// a name: the voice is theirs, the words say so, the voice is learned
	out := a.ok(a.alice, "PUT", base+"/speakers/S1", map[string]any{"name": " Alex "})
	if sp := speakerIn(out, "S1"); sp["name"] != "Alex" || sp["state"] != "confirmed" {
		t.Fatalf("S1: %v", sp)
	}
	if !strings.Contains(out["transcript"].(string), "\nAlex: We ship Friday.") || !strings.HasPrefix(out["tidy"].(string), "Alex: We ship Friday.") || out["summary"] != "Alex wants to ship on Friday." {
		t.Fatalf("words: %q / %q / %q", out["transcript"], out["tidy"], out["summary"])
	}
	people := a.ok(a.alice, "GET", "/api/v1/people", nil)["items"].([]any)
	if len(people) != 1 || people[0].(map[string]any)["name"] != "Alex" || people[0].(map[string]any)["voices"].(float64) != 1 {
		t.Fatalf("people: %v", people)
	}
	if _, m2, _ := a.call(a.bob, "PUT", base+"/speakers/S2", map[string]any{"name": "Bob's guess"}); m2["error"] != "not found" {
		t.Fatalf("another person's memo: %v", m2)
	}

	// the next meeting: Alex's voice is recognised, Heidi's is not known yet
	m2 := meeting(t, a, voice(2, 5, 0.2), voice(1, 6, 0.25))
	c := context.Background()
	got, _ := a.st.GetMemo(c, a.ids["Alice"], m2.ID)
	a.s.nameVoices(c, a.ids["Alice"], got)
	if sp := speakerOf(got, "S2"); sp.State != store.SpeakerAuto || sp.Name != "Alex" {
		t.Fatalf("recognised: %+v", sp)
	}
	if sp := speakerOf(got, "S1"); sp.State != store.SpeakerUnknown {
		t.Fatalf("unknown: %+v", sp)
	}
	_ = a.st.SaveMemo(c, a.ids["Alice"], got)
	base2 := "/api/v1/memos/" + itoa(m2.ID)
	// "not Alex": the guess goes, and is not made again
	out = a.ok(a.alice, "PUT", base2+"/speakers/S2", map[string]any{"answer": "no"})
	if sp := speakerIn(out, "S2"); sp["state"] != "unknown" || sp["name"] != "" || len(sp["rejected"].([]any)) != 1 {
		t.Fatalf("rejected: %v", sp)
	}
	// both voices named the same person: one speaker
	a.ok(a.alice, "PUT", base2+"/speakers/S1", map[string]any{"name": "Heidi"})
	out = a.ok(a.alice, "PUT", base2+"/speakers/S2", map[string]any{"name": "heidi"})
	if len(out["speakers"].([]any)) != 2 || speakerIn(out, "S1")["name"] != "Heidi" || !strings.Contains(out["transcript"].(string), "Heidi: We ship Friday. Agreed. Me too.") {
		t.Fatalf("merged: %v %q", out["speakers"], out["transcript"])
	}
	// who really said it
	out = a.ok(a.alice, "POST", base2+"/segments/speaker", map[string]any{"indexes": []int{3}, "speaker": ""})
	if sp := speakerIn(out, "S2"); sp == nil || sp["state"] != "unknown" || sp["sample"].([]any)[0].(float64) != 8000 || !strings.HasSuffix(out["transcript"].(string), "Speaker 2: Me too.") {
		t.Fatalf("moved: %v %q", out["speakers"], out["transcript"])
	}
	a.expect(http.StatusBadRequest, a.alice, "POST", base2+"/segments/speaker", map[string]any{"indexes": []int{99}, "speaker": "S1"})

	// renaming someone renames them everywhere; forgetting them takes the name off
	var heidi float64
	for _, p := range a.ok(a.alice, "GET", "/api/v1/people", nil)["items"].([]any) {
		if p.(map[string]any)["name"] == "Heidi" {
			heidi = p.(map[string]any)["id"].(float64)
		}
	}
	a.ok(a.alice, "PUT", "/api/v1/people/"+itoa(int64(heidi)), map[string]any{"name": "Hedy"})
	if out = a.ok(a.alice, "GET", base2, nil); !strings.Contains(out["transcript"].(string), "Hedy: We ship Friday.") {
		t.Fatalf("renamed: %q", out["transcript"])
	}
	a.expect(http.StatusNoContent, a.alice, "DELETE", "/api/v1/people/"+itoa(int64(heidi)), nil)
	if out = a.ok(a.alice, "GET", base2, nil); speakerIn(out, "S1")["name"] != "" || !strings.Contains(out["transcript"].(string), "Speaker 1: We ship Friday.") {
		t.Fatalf("forgotten: %v %q", speakerIn(out, "S1"), out["transcript"])
	}
	// taking a name off
	out = a.ok(a.alice, "PUT", base+"/speakers/S1", map[string]any{"name": ""})
	if sp := speakerIn(out, "S1"); sp["state"] != "unknown" || !strings.Contains(out["transcript"].(string), "Speaker 1: We ship") {
		t.Fatalf("unnamed: %v", sp)
	}
	a.expect(http.StatusBadRequest, a.alice, "PUT", base+"/speakers/S1", map[string]any{"name": "You"})
	a.expect(http.StatusNotFound, a.alice, "PUT", base+"/speakers/You", map[string]any{"name": "Me"})
}

// speakerTools is meetingTools with a speaker finder that hears two voices
// on the call: the first second and the second one.
func speakerTools(t *testing.T) func(*config.Config) {
	base := meetingTools(t, true, true)
	dir := t.TempDir()
	py := filepath.Join(dir, "python3")
	_ = os.WriteFile(py, []byte(`#!/bin/sh
[ "$1" = "-c" ] && exit 0
cat >/dev/null
echo '{"turns":[{"s":0,"e":950,"k":0},{"s":1000,"e":2000,"k":1}],"speakers":[{"k":0,"ms":950,"n":2,"spread":0.1,"emb":[1,0,0]},{"k":1,"ms":1000,"n":2,"spread":0.1,"emb":[0,1,0]}],"dim":3}'
`), 0o755)
	return func(c *config.Config) {
		base(c)
		c.SpeakersPython = py
		_ = os.MkdirAll(filepath.Join(c.ModelsDir, "speakers"), 0o755)
		for _, m := range speakers.Models {
			_ = os.WriteFile(filepath.Join(c.ModelsDir, "speakers", m.File), make([]byte, 2<<20), 0o644)
		}
	}
}

func TestMeetingVoicesAreFound(t *testing.T) {
	a := newApp(t, speakerTools(t))
	_, m := a.upload(a.alice, map[string]string{"meeting": "1"}, "call.m4a", []byte("m4a"))
	done := a.waitDone(a.alice, id(m))
	if done["status"] != "done" || done["speakers_status"] != "done" {
		t.Fatalf("not done: %v %v %v", done["status"], done["speakers_status"], done["speakers_error"])
	}
	if sp := done["speakers"].([]any); len(sp) != 3 || speakerIn(done, "S1") == nil || speakerIn(done, "S2") == nil {
		t.Fatalf("speakers: %v", sp)
	}
	tr := done["transcript"].(string)
	if !strings.Contains(tr, "Speaker 1: Yes, Friday works.") || !strings.Contains(tr, "Speaker 2: Good, we agree.") {
		t.Fatalf("transcript: %q", tr)
	}
	// name one; finding the voices again (the button) keeps the name
	a.ok(a.alice, "PUT", "/api/v1/memos/"+id(m)+"/speakers/S2", map[string]any{"name": "Arno"})
	a.expect(http.StatusAccepted, a.alice, "POST", "/api/v1/memos/"+id(m)+"/speakers/find", nil)
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := a.ok(a.alice, "GET", "/api/v1/memos/"+id(m), nil)
		if got["speakers_status"] == "done" {
			if sp := speakerIn(got, "S2"); sp["name"] != "Arno" || sp["state"] != "confirmed" || !strings.Contains(got["transcript"].(string), "Arno: Good, we agree.") {
				t.Fatalf("found again: %v %q", got["speakers"], got["transcript"])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not found again: %v %v", got["speakers_status"], got["speakers_error"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	// an ordinary memo cannot be split
	b := newApp(t, fakeTools(t, "A note.|More.", "en"))
	_, m3 := b.upload(b.alice, nil, "n.webm", []byte("webm"))
	b.waitDone(b.alice, id(m3))
	b.expect(http.StatusBadRequest, b.alice, "POST", "/api/v1/memos/"+id(m3)+"/speakers/find", nil)
}

func TestNameHints(t *testing.T) {
	var asked string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		asked = string(raw)
		content := `{"speakers":[{"label":"Speaker 2","name":"Heidi","why":"At 0:06 Speaker 1 says \"Heidi?\" and Speaker 2 answers."},{"label":"Speaker 1","name":"Alex","why":"named already"},{"label":"Speaker 9","name":"Nobody","why":"no such voice"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer fake.Close()
	a := newApp(t, nil)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"ai_provider": "openai", "openai_api_key": "sk-test", "openai_base_url": fake.URL, "openai_model": "m"})
	m := meeting(t, a, voice(1, 0, 0), voice(2, 0, 0))
	a.ok(a.alice, "PUT", "/api/v1/memos/"+itoa(m.ID)+"/speakers/S1", map[string]any{"name": "Alex"})
	if err := a.s.hints(context.Background(), a.ids["Alice"], m.ID); err != nil {
		t.Fatal(err)
	}
	got := a.ok(a.alice, "GET", "/api/v1/memos/"+itoa(m.ID), nil)
	if sp := speakerIn(got, "S2"); sp["hint"] != "Heidi" || !strings.Contains(sp["hint_why"].(string), "0:06") || sp["name"] != "" {
		t.Fatalf("hint: %v", sp)
	}
	if sp := speakerIn(got, "S1"); sp["hint"] != nil {
		t.Fatalf("a named voice got a hint: %v", sp)
	}
	if !strings.Contains(asked, `[0:03] Alex: We ship Friday.`) || !strings.Contains(asked, `Voices already known: Alex`) {
		t.Fatalf("asked: %s", asked)
	}
}

func TestOldTidyIsFound(t *testing.T) {
	a := newApp(t, nil)
	c := context.Background()
	old := meeting(t, a, voice(1, 0, 0), voice(2, 0, 0))
	old.Tidy = "You: Shall we ship?\nOthers: We ship Friday."
	_ = a.st.SaveMemo(c, a.ids["Alice"], old)
	fresh := meeting(t, a, voice(1, 0, 0), voice(2, 0, 0)) // its tidy-up names the voices
	if !oldTidy(old) || oldTidy(fresh) {
		t.Fatal("oldTidy")
	}
	list, err := a.st.MeetingsWithOldTidy(c)
	if err != nil || len(list) != 1 || list[0].ID != old.ID {
		t.Fatalf("old tidy-ups: %v %v", list, err)
	}
}
