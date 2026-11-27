package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/config"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// capScopes are the scopes of the capability tests' link tokens:
// pcx_<who>_<name> acts for <who>@example.com with that one scope, sent by
// Pocket Hub.
var capScopes = map[string]string{
	"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
	"read": "voice:read", "write": "voice:write", "transcribe": "voice:transcribe",
}

// capCore is Pocket Core for the capability tests: it answers introspection
// for the link tokens above and keeps the manifest Voice registers with.
type capCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func newCapCore(t *testing.T) *capCore {
	t.Helper()
	c := &capCore{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/api/v1/apps/register":
			c.mu.Lock()
			c.manifest = body
			c.mu.Unlock()
			io.WriteString(w, `{"app":{}}`)
		case "/api/v1/auth/introspect":
			tok, _ := body["token"].(string)
			parts := strings.SplitN(strings.TrimPrefix(tok, "pcx_"), "_", 2)
			if len(parts) != 2 || capScopes[parts[1]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			email := parts[0] + "@example.com"
			fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"voice","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"hub","name":"Pocket Hub"}}`,
				capScopes[parts[1]], email, email, parts[0])
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(c.Close)
	return c
}

// memo keeps a memo for a person straight in the store, written out unless said.
func (a *app) memo(who string, m store.Memo, at time.Time) int64 {
	a.t.Helper()
	if m.Status == "" {
		m.Status = store.StatusDone
	}
	m.RecordedAt = at.UTC().Format(time.RFC3339Nano)
	if err := a.st.CreateMemo(context.Background(), a.ids[who], &m); err != nil {
		a.t.Fatal(err)
	}
	return m.ID
}

// get reads a capability route into v.
func (a *app) get(tok, path string, v any) int {
	a.t.Helper()
	code, body := a.raw(tok, "GET", path, "", nil)
	if v != nil && code == 200 {
		if err := json.Unmarshal(body, v); err != nil {
			a.t.Fatalf("%s: %v (%s)", path, err, body)
		}
	}
	return code
}

// Pocket Search's route: memos, meetings and dictations by their words, a
// match in the title first, a short plain part of the text with the length
// and the day, and only the person's own.
func TestPocketSearch(t *testing.T) {
	a := newApp(t, nil)
	now := time.Now().Add(-time.Hour)
	titled := a.memo("Alice", store.Memo{Title: "Dentist on Friday", Transcript: "Book it before lunch.", DurationMS: 21000}, now.Add(-48*time.Hour))
	worded := a.memo("Alice", store.Memo{Title: "Errands", Transcript: "Pick up the dry cleaning, then call the dentist about the crown, and buy bread on the way home tonight please.", DurationMS: 95000}, now)
	meeting := a.memo("Alice", store.Memo{Title: "Team call", Meeting: true, Kind: "meeting", Summary: "Ana goes to the dentist on Monday, the launch moves.", DurationMS: 45 * 60000}, now.Add(-24*time.Hour))
	a.memo("Alice", store.Memo{Title: "Groceries", Transcript: "milk and eggs"}, now)
	a.memo("Bob", store.Memo{Title: "Bob's dentist", Transcript: "dentist dentist"}, now)
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("search-dictation-1", "Remind me to ring the dentist", "Messages", 2400, now))

	var res core.SearchResults
	if code := a.get(a.alice, "/api/v1/pocket/search?q=Dentist", &res); code != 200 || len(res.Items) != 4 {
		t.Fatalf("search: %d %+v", code, res)
	}
	first := res.Items[0]
	if first.ID != "memo:"+itoa(titled) || first.Title != "Dentist on Friday" || first.Kind != "memo" || first.URL != "/memos/"+itoa(titled) {
		t.Errorf("first: %+v", first)
	}
	if _, err := time.Parse(time.RFC3339, first.Date); err != nil {
		t.Errorf("date %q: %v", first.Date, err)
	}
	if !strings.Contains(first.Snippet, "Book it before lunch.") || !strings.Contains(first.Snippet, "21 s") {
		t.Errorf("title match snippet: %q", first.Snippet)
	}
	byID := map[string]core.SearchItem{}
	for i, it := range res.Items {
		byID[it.ID] = it
		if strings.Contains(it.Title, "Bob") {
			t.Errorf("Bob's memo in Alice's search: %+v", it)
		}
		if strings.ContainsAny(it.Snippet, "\n") || len([]rune(it.Snippet)) > 200 {
			t.Errorf("snippet not short and plain: %q", it.Snippet)
		}
		if i > 0 && it.Score >= res.Items[i-1].Score {
			t.Errorf("scores not falling: %v", res.Items)
		}
	}
	if w := byID["memo:"+itoa(worded)]; !strings.Contains(w.Snippet, "call the dentist about") || !strings.HasPrefix(w.Snippet, "…") || !strings.Contains(w.Snippet, " · 2 min · ") {
		t.Errorf("match in the words: %q", w.Snippet)
	}
	if m := byID["memo:"+itoa(meeting)]; m.Kind != "meeting" || !strings.Contains(m.Snippet, "dentist on Monday") || !strings.Contains(m.Snippet, "45 min") {
		t.Errorf("meeting: %+v", m)
	}
	last := res.Items[3]
	if last.Kind != "dictation" || !strings.HasPrefix(last.ID, "dictation:") || last.Title != "Remind me to ring the dentist" ||
		last.URL != "/dictation?q=Dentist" || !strings.Contains(last.Snippet, "into Messages") {
		t.Errorf("dictation: %+v", last)
	}

	res = core.SearchResults{}
	if a.get(a.alice, "/api/v1/pocket/search?q=dentist&limit=1", &res); len(res.Items) != 1 || res.Items[0].ID != "memo:"+itoa(titled) {
		t.Errorf("limit=1: %+v", res.Items)
	}
	for _, q := range []string{"", "%20", "%3F%3F", "*", "zebra"} {
		var raw map[string]any
		if code := a.get(a.alice, "/api/v1/pocket/search?q="+q, &raw); code != 200 {
			t.Errorf("q=%q: %d", q, code)
		}
		if items, ok := raw["items"].([]any); !ok || len(items) != 0 {
			t.Errorf("q=%q: %v", q, raw)
		}
	}
	if code := a.get("", "/api/v1/pocket/search?q=dentist", nil); code != 401 {
		t.Errorf("without a sign-in: %d", code)
	}
}

// Pocket Day's route: the memos and meetings recorded on the caller's day and
// written out, who was on a call or the folder, the summary as the note,
// dictations as numbers, and nobody else's.
func TestPocketDay(t *testing.T) {
	a := newApp(t, nil)
	tz, err := time.LoadLocation("Europe/Sarajevo")
	if err != nil {
		t.Skip("no time zone data here")
	}
	at := func(day, hour, min int) time.Time { return time.Date(2026, 3, day, hour, min, 0, 0, tz) }
	work, err := a.st.CreateFolder(context.Background(), a.ids["Alice"], "Work")
	if err != nil {
		t.Fatal(err)
	}
	memo := a.memo("Alice", store.Memo{Title: "Shopping list", Summary: "Milk, eggs and bread.", DurationMS: 90000, FolderID: work.ID}, at(10, 9, 0))
	call := a.memo("Alice", store.Memo{Title: "", Meeting: true, Kind: "meeting", DurationMS: 45 * 60000, Speakers: []store.Speaker{
		{Key: "You", State: store.SpeakerYou}, {Key: "S1", Name: "Alex", State: store.SpeakerConfirmed}, {Key: "S2", Name: "Mia", State: store.SpeakerAuto}, {Key: "S3", State: store.SpeakerUnknown},
	}}, at(10, 14, 0))
	night := a.memo("Alice", store.Memo{Title: "After midnight", Transcript: "00:30 in Sarajevo is still the 9th in UTC"}, at(10, 0, 30))
	a.memo("Alice", store.Memo{Title: "The day before"}, at(9, 12, 0))
	a.memo("Alice", store.Memo{Title: "Still being written out", Status: store.StatusWorking}, at(10, 16, 0))
	a.memo("Bob", store.Memo{Title: "Bob's day"}, at(10, 10, 0))
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("day-dictation-01", words(12), "Notes", 4000, at(10, 11, 0)))
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("day-dictation-02", words(30), "Mail", 9000, at(10, 18, 0)))
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("day-dictation-03", words(5), "Mail", 2000, at(11, 9, 0)))
	a.expect(http.StatusCreated, a.bob, "POST", "/api/v1/dictations", dict("day-dictation-04", words(7), "Mail", 2000, at(10, 9, 0)))

	var day core.Day
	if code := a.get(a.alice, "/api/v1/pocket/day/2026-03-10?tz=Europe/Sarajevo", &day); code != 200 {
		t.Fatalf("day: %d", code)
	}
	if day.Date != "2026-03-10" || day.Summary != "2 memos, 1 meeting (45 min)" || len(day.Items) != 3 {
		t.Fatalf("day: %+v", day)
	}
	got := map[string]core.DayItem{}
	for _, it := range day.Items {
		got[it.ID] = it
	}
	if m := got[itoa(memo)]; m.Time != "09:00" || m.Title != "Shopping list" || m.Kind != "memo" || m.Sub != "Work" || m.Minutes != 1.5 ||
		m.Note != "Milk, eggs and bread." || m.URL != "/memos/"+itoa(memo) {
		t.Errorf("memo: %+v", m)
	}
	if c := got[itoa(call)]; c.Time != "14:00" || c.Title != "Meeting" || c.Kind != "meeting" || c.Sub != "with Alex and Mia" || c.Minutes != 45 || c.Note != "" {
		t.Errorf("meeting: %+v", c)
	}
	if n := got[itoa(night)]; n.Time != "00:30" {
		t.Errorf("after midnight: %+v", n)
	}
	if len(day.Metrics) != 2 || day.Metrics[0] != (core.Metric{Label: "Dictations", Value: "2"}) || day.Metrics[1] != (core.Metric{Label: "Words dictated", Value: "42"}) {
		t.Errorf("metrics: %+v", day.Metrics)
	}

	// Only dictations that day: the line says so. Nothing: nothing.
	day = core.Day{}
	if a.get(a.alice, "/api/v1/pocket/day/2026-03-11?tz=Europe/Sarajevo", &day); day.Summary != "1 dictation" || len(day.Items) != 0 || len(day.Metrics) != 2 {
		t.Errorf("a day of dictation: %+v", day)
	}
	var raw map[string]any
	if a.get(a.alice, "/api/v1/pocket/day/2026-03-20?tz=Europe/Sarajevo", &raw); raw["summary"] != nil || len(raw["items"].([]any)) != 0 {
		t.Errorf("an empty day: %v", raw)
	}
	day = core.Day{}
	if a.get(a.bob, "/api/v1/pocket/day/2026-03-10?tz=Europe/Sarajevo", &day); len(day.Items) != 1 || day.Items[0].Title != "Bob's day" || day.Summary != "1 memo" {
		t.Errorf("Bob's day: %+v", day)
	}
	if code := a.get(a.alice, "/api/v1/pocket/day/10-03-2026", nil); code != 400 {
		t.Errorf("a bad date: %d", code)
	}
}

// Pocket Hub's widget: what is being written out first, then memos with
// suggestions to send on, then the newest; today's count as the line.
func TestPocketWidget(t *testing.T) {
	a := newApp(t, nil)
	var w core.WidgetData
	var raw map[string]any
	if code := a.get(a.alice, "/api/v1/pocket/widget", &raw); code != 200 || raw["line"] != nil || len(raw["items"].([]any)) != 0 {
		t.Fatalf("nothing yet: %d %v", code, raw)
	}

	now := time.Now()
	busy := a.memo("Alice", store.Memo{Title: "Long interview", Status: store.StatusWorking, DurationMS: 30 * 60000}, now.Add(-72*time.Hour))
	plain := a.memo("Alice", store.Memo{Title: "A thought", DurationMS: 30000}, now.Add(-time.Minute))
	waiting := a.memo("Alice", store.Memo{Title: "Plans", DurationMS: 120000, Actions: []store.Action{
		{Type: "task", Title: "Call the plumber"}, {Type: "task", Title: "Buy paint", SentTo: "tasks"}, {Type: "event", Title: "Dinner", Skipped: true}, {Type: "task", Title: "Water plants"},
	}}, now.Add(-2*time.Minute))
	a.memo("Bob", store.Memo{Title: "Bob's memo"}, now)

	if code := a.get(a.alice, "/api/v1/pocket/widget", &w); code != 200 || len(w.Items) != 3 {
		t.Fatalf("widget: %d %+v", code, w)
	}
	if w.Line != "2 memos today, 1 being written out" || w.URL != "/" {
		t.Errorf("line %q url %q", w.Line, w.URL)
	}
	if w.Items[0].ID != itoa(busy) || !strings.HasPrefix(w.Items[0].Detail, "30 min · ") || !strings.HasSuffix(w.Items[0].Detail, " · being written out") {
		t.Errorf("being written out first: %+v", w.Items[0])
	}
	if w.Items[1].ID != itoa(waiting) || !strings.HasPrefix(w.Items[1].Detail, "2 min · today ") || !strings.HasSuffix(w.Items[1].Detail, " · 2 to send on") {
		t.Errorf("waiting to be sent on second: %+v", w.Items[1])
	}
	if w.Items[2].ID != itoa(plain) || w.Items[2].Title != "A thought" || w.Items[2].URL != "/memos/"+itoa(plain) || !strings.HasPrefix(w.Items[2].Detail, "30 s · today ") {
		t.Errorf("newest: %+v", w.Items[2])
	}

	w = core.WidgetData{}
	if a.get(a.bob, "/api/v1/pocket/widget", &w); len(w.Items) != 1 || w.Items[0].Title != "Bob's memo" || w.Line != "1 memo today" {
		t.Errorf("Bob's widget: %+v", w)
	}
}

func TestVoiceDaySummary(t *testing.T) {
	for _, c := range []struct {
		memos, meetings int
		ms              int64
		dictations      int
		want            string
	}{
		{0, 0, 0, 0, ""},
		{1, 0, 0, 4, "1 memo"},
		{2, 1, 45 * 60000, 0, "2 memos, 1 meeting (45 min)"},
		{0, 2, 95 * 60000, 0, "2 meetings (1 h 35 min)"},
		{0, 1, 0, 0, "1 meeting"},
		{0, 0, 0, 3, "3 dictations"},
	} {
		if got := voiceDaySummary(c.memos, c.meetings, c.ms, c.dictations); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
	for ms, want := range map[int64]string{0: "", 400: "0 s", 45_000: "45 s", 89_000: "1 min", 6*60000 + 15000: "6 min", 65 * 60000: "1 h 5 min"} {
		if got := spokenLength(ms); got != want {
			t.Errorf("spokenLength(%d) = %q, want %q", ms, got, want)
		}
	}
}

// Through a link token each pocket:* scope reaches its one route and
// /auth/me, nothing else; voice:read and the other old scopes do not open
// the new routes. The link acts for one person.
func TestPocketLinkScopes(t *testing.T) {
	c := newCapCore(t)
	a := newApp(t, func(cfg *config.Config) { cfg.CoreURL = c.URL })
	a.memo("Alice", store.Memo{Title: "Dentist on Friday"}, time.Now().Add(-time.Minute))

	routes := []struct{ scope, path string }{
		{"search", "/api/v1/pocket/search?q=dentist"},
		{"day", "/api/v1/pocket/day/" + time.Now().Format("2006-01-02")},
		{"widget", "/api/v1/pocket/widget"},
	}
	for _, tok := range []string{"search", "day", "widget", "capture", "read", "write", "transcribe"} {
		for _, rt := range routes {
			want := http.StatusForbidden
			if tok == rt.scope {
				want = http.StatusOK
			}
			if code, body := a.raw("pcx_alice_"+tok, "GET", rt.path, "", nil); code != want {
				t.Errorf("GET %s with %s: %d, want %d (%s)", rt.path, capScopes[tok], code, want, body)
			}
		}
	}
	for _, tok := range []string{"search", "day", "widget", "capture"} {
		for _, rt := range []struct{ method, path string }{
			{"GET", "/api/v1/pocket/day/today"},
			{"GET", "/api/v1/memos"}, {"GET", "/api/v1/search?q=dentist"}, {"GET", "/api/v1/day/2026-03-10"}, {"GET", "/api/v1/summary"},
			{"GET", "/api/v1/dictations"}, {"POST", "/api/v1/memos"}, {"POST", "/api/v1/memos/1/send"}, {"POST", "/api/v1/memos/1/tidy"},
			{"DELETE", "/api/v1/memos/1"}, {"POST", "/api/v1/transcribe"}, {"GET", "/api/v1/settings"}, {"GET", "/api/v1/export.json"},
			{"GET", core.PortablePath},
		} {
			if code, _ := a.raw("pcx_alice_"+tok, rt.method, rt.path, "application/json", []byte("{}")); code != http.StatusForbidden {
				t.Errorf("%s %s with %s: %d, want 403", rt.method, rt.path, capScopes[tok], code)
			}
		}
		code, body := a.raw("pcx_alice_"+tok, "GET", "/api/v1/auth/me", "", nil)
		if code != 200 || !strings.Contains(string(body), "alice@example.com") {
			t.Errorf("auth/me with %s: %d %s", capScopes[tok], code, body)
		}
	}

	var res core.SearchResults
	if a.get("pcx_alice_search", "/api/v1/pocket/search?q=dentist", &res); len(res.Items) != 1 {
		t.Errorf("alice through the link finds %d", len(res.Items))
	}
	res = core.SearchResults{}
	if a.get("pcx_bob_search", "/api/v1/pocket/search?q=dentist", &res); len(res.Items) != 0 {
		t.Errorf("bob through the link finds %d", len(res.Items))
	}
	var w core.WidgetData
	if a.get("pcx_bob_widget", "/api/v1/pocket/widget", &w); len(w.Items) != 0 {
		t.Errorf("bob through the link sees %+v", w.Items)
	}
}

// What Core is told: search, day and widget (no capture), their offers next
// to the app's own, and the shelf the app sits on.
func TestManifestCapabilities(t *testing.T) {
	c := newCapCore(t)
	a := newApp(t, func(cfg *config.Config) {
		cfg.CoreURL, cfg.CoreRegistrationKey, cfg.CoreAppURL = c.URL, "pcr_test", "http://voice:8080"
	})
	if err := a.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	raw, _ := json.Marshal(c.manifest)
	c.mu.Unlock()
	var m core.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Slug != "voice" || m.Category != "Everyday" {
		t.Errorf("manifest %q on shelf %q", m.Slug, m.Category)
	}
	if m.Search == nil || strings.Join(m.Search.Bangs, ",") != "voice,memo,memos" || strings.Join(m.Search.Kinds, ",") != "memo,meeting,dictation" || m.Search.What == "" {
		t.Errorf("search: %+v", m.Search)
	}
	if m.Day == nil || m.Day.Label != "Voice" || m.Day.One != "memo" || m.Day.Many != "memos" || m.Day.What == "" {
		t.Errorf("day: %+v", m.Day)
	}
	if m.Widget == nil || m.Widget.Title != "Voice memos" || m.Widget.What == "" {
		t.Errorf("widget: %+v", m.Widget)
	}
	if m.Capture != nil {
		t.Errorf("capture declared: %+v", m.Capture)
	}
	offers := map[string]bool{}
	for _, o := range m.Offers {
		if o.Name == "" || o.Description == "" {
			t.Errorf("offer %q has no words", o.Scope)
		}
		offers[o.Scope] = true
	}
	for _, scope := range []string{core.ScopeSearch, core.ScopeDay, core.ScopeWidget, "voice:read", "voice:write", "voice:transcribe", core.ScopeMoveExport, core.ScopeMoveImport} {
		if !offers[scope] {
			t.Errorf("offer %s is missing", scope)
		}
	}
	if offers[core.ScopeCapture] {
		t.Error("pocket:capture offered without a capture")
	}
}
