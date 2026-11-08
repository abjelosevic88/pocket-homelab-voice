package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func words(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

// onDay is a time on a local day n days from today.
func onDay(n, hour int) time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+n, hour, 0, 1, 0, time.Local)
}

func dict(ref, text, app string, ms int64, at time.Time) map[string]any {
	return map[string]any{"ref": ref, "text": text, "app": app, "app_id": "org.example." + strings.ToLower(strings.ReplaceAll(app, " ", "")), "duration_ms": ms,
		"engine": "parakeet-v3", "language": "en", "device": "Aleksandar’s MacBook Pro", "dictated_at": at.Format(time.RFC3339)}
}

func TestDictationIsIdempotent(t *testing.T) {
	a := newApp(t, nil)
	in := dict("6f1c0e2a-1111-4b6a-9c3e-000000000001", "  Send the report to Ana tomorrow  ", "Pocket Projects", 3200, time.Now().Add(-time.Hour))
	in["raw"], in["polished"] = "uh send the report to ana tomorrow", true
	first := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", in)
	d, _ := first["dictation"].(map[string]any)
	if d == nil || d["text"] != "Send the report to Ana tomorrow" || num(d, "words") != 6 || d["polished"] != true || d["app"] != "Pocket Projects" ||
		d["raw"] != "uh send the report to ana tomorrow" || num(d, "duration_ms") != 3200 || d["created_at"] == "" {
		t.Fatalf("created: %v", first)
	}
	for _, k := range []string{"id", "ref", "text", "raw", "app", "app_id", "duration_ms", "words", "engine", "language", "device", "source", "polished", "dictated_at", "created_at"} {
		if _, ok := d[k]; !ok {
			t.Fatalf("dictation has no %q: %v", k, d)
		}
	}
	if _, err := time.Parse(time.RFC3339, d["dictated_at"].(string)); err != nil {
		t.Fatalf("dictated_at: %v", d["dictated_at"])
	}
	// the outbox sends it again (even changed): the stored one comes back
	in["text"] = "something else"
	again := a.expect(http.StatusOK, a.alice, "POST", "/api/v1/dictations", in)
	d2, _ := again["dictation"].(map[string]any)
	if id(d2) != id(d) || d2["text"] != d["text"] {
		t.Fatalf("second POST: %v, first %v", again, first)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/dictations", nil); num(l, "total") != 1 {
		t.Fatalf("list after two POSTs: %v", l)
	}
	// refs are per person: Bob's own dictation with the same ref is new
	b := a.expect(http.StatusCreated, a.bob, "POST", "/api/v1/dictations", in)
	if id(b["dictation"].(map[string]any)) == id(d) {
		t.Fatalf("Bob got Alice's dictation: %v", b)
	}
}

func TestDictationValidation(t *testing.T) {
	a := newApp(t, nil)
	ok := func() map[string]any { return dict("r-valid", "hello there", "Slack", 1000, time.Now()) }
	cases := map[string]func(map[string]any){
		"no ref":          func(m map[string]any) { delete(m, "ref") },
		"long ref":        func(m map[string]any) { m["ref"] = strings.Repeat("x", 65) },
		"empty text":      func(m map[string]any) { m["text"] = "" },
		"blank text":      func(m map[string]any) { m["text"] = " \n\t " },
		"long text":       func(m map[string]any) { m["text"] = strings.Repeat("a", 20001) },
		"negative length": func(m map[string]any) { m["duration_ms"] = -1 },
		"future":          func(m map[string]any) { m["dictated_at"] = time.Now().Add(10 * time.Minute).Format(time.RFC3339) },
		"no offset":       func(m map[string]any) { m["dictated_at"] = "2026-11-05T10:00:00" },
	}
	for name, change := range cases {
		m := ok()
		m["ref"] = "r-" + strings.ReplaceAll(name, " ", "-")
		change(m)
		code, body, _ := a.call(a.alice, "POST", "/api/v1/dictations", m)
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", name, code, body)
		}
	}
	if l := a.ok(a.alice, "GET", "/api/v1/dictations", nil); num(l, "total") != 0 {
		t.Fatalf("invalid ones were kept: %v", l)
	}
	// a clock a little ahead is fine; no time means now
	m := ok()
	m["dictated_at"] = time.Now().Add(2 * time.Minute).Format(time.RFC3339)
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", m)
	m = ok()
	m["ref"], m["dictated_at"] = "r-now", ""
	d := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", m)["dictation"].(map[string]any)
	if at, _ := time.Parse(time.RFC3339, d["dictated_at"].(string)); time.Since(at) > time.Minute || time.Since(at) < -time.Second {
		t.Fatalf("default dictated_at: %v", d["dictated_at"])
	}
	m = ok()
	m["ref"], m["text"] = "r-max", strings.Repeat("é", 20000)
	a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", m)
}

func TestDictationIsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	d := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("r1", "only mine", "Notes", 1000, time.Now()))["dictation"].(map[string]any)
	did := id(d)
	a.expect(http.StatusNotFound, a.bob, "DELETE", "/api/v1/dictations/"+did, nil)
	a.expect(http.StatusNotFound, a.alice, "DELETE", "/api/v1/dictations/999999", nil)
	if l := a.ok(a.bob, "GET", "/api/v1/dictations", nil); num(l, "total") != 0 || len(l["items"].([]any)) != 0 {
		t.Fatalf("Bob sees Alice's dictations: %v", l)
	}
	if st := a.ok(a.bob, "GET", "/api/v1/dictations/stats", nil); num(st, "sessions") != 0 || num(st, "words") != 0 {
		t.Fatalf("Bob's stats count Alice's: %v", st)
	}
	if r := a.ok(a.bob, "DELETE", "/api/v1/dictations", nil); num(r, "deleted") != 0 {
		t.Fatalf("Bob cleared: %v", r)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/dictations", nil); num(l, "total") != 1 {
		t.Fatalf("Alice lost hers: %v", l)
	}
	a.expect(http.StatusNoContent, a.alice, "DELETE", "/api/v1/dictations/"+did, nil)
	a.expect(http.StatusNotFound, a.alice, "DELETE", "/api/v1/dictations/"+did, nil)
	for i := 0; i < 3; i++ {
		a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("c"+itoa(int64(i)), "one two", "Notes", 0, time.Now()))
	}
	if r := a.ok(a.alice, "DELETE", "/api/v1/dictations", nil); num(r, "deleted") != 3 {
		t.Fatalf("clear: %v", r)
	}
	// signed out: nothing
	a.expect(http.StatusUnauthorized, "", "GET", "/api/v1/dictations/stats", nil)
	a.expect(http.StatusUnauthorized, "", "POST", "/api/v1/dictations", dict("x", "y", "z", 0, time.Now()))
}

func TestDictationListFilters(t *testing.T) {
	a := newApp(t, nil)
	post := func(ref, text, app string, at time.Time) {
		a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict(ref, text, app, 1000, at))
	}
	// posted out of order (an outbox catching up): the list goes by when it was said
	post("d3", "Ćevapi for dinner tonight", "Messages", onDay(-1, 19))
	post("d1", "Standup notes for the team", "Slack", onDay(-3, 9))
	post("d5", "Reply to the landlord about the heating", "Mail", onDay(0, 0))
	post("d2", "Ship it on Friday", "Slack", onDay(-2, 10))
	post("d4", "Buy cevapcici and bread", "Messages", onDay(-1, 8))

	refs := func(l map[string]any) string {
		var out []string
		for _, x := range l["items"].([]any) {
			out = append(out, x.(map[string]any)["ref"].(string))
		}
		return strings.Join(out, ",")
	}
	if l := a.ok(a.alice, "GET", "/api/v1/dictations", nil); refs(l) != "d5,d3,d4,d2,d1" || num(l, "total") != 5 || num(l, "next") != 0 {
		t.Fatalf("all: %s %v", refs(l), l["next"])
	}
	// accents and case do not matter
	if l := a.ok(a.alice, "GET", "/api/v1/dictations?q=CEVAP", nil); refs(l) != "d3,d4" || num(l, "total") != 2 {
		t.Fatalf("q: %s", refs(l))
	}
	// the search behind the palette finds dictations next to the memos
	if l := a.ok(a.alice, "GET", "/api/v1/search?q=cevap", nil); num(l, "total") != 0 || num(num2(l, "dictations"), "total") != 2 || len(num2(l, "dictations")["items"].([]any)) != 2 {
		t.Fatalf("search: %v", l)
	}
	if l := a.ok(a.bob, "GET", "/api/v1/search?q=cevap", nil); num(num2(l, "dictations"), "total") != 0 {
		t.Fatalf("Bob's search finds Alice's dictations: %v", l)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/dictations?app=Slack", nil); refs(l) != "d2,d1" {
		t.Fatalf("app: %s", refs(l))
	}
	y := onDay(-1, 12).Format("2006-01-02")
	if l := a.ok(a.alice, "GET", "/api/v1/dictations?from="+y+"&to="+y, nil); refs(l) != "d3,d4" {
		t.Fatalf("from/to: %s", refs(l))
	}
	a.expect(http.StatusBadRequest, a.alice, "GET", "/api/v1/dictations?from=yesterday", nil)
	// pages of two with the cursor
	var all []string
	path := "/api/v1/dictations?limit=2"
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("the cursor never ends")
		}
		l := a.ok(a.alice, "GET", path, nil)
		if num(l, "total") != 5 {
			t.Fatalf("total on a page: %v", l["total"])
		}
		all = append(all, refs(l))
		next := int64(num(l, "next"))
		if next == 0 {
			break
		}
		path = "/api/v1/dictations?limit=2&before=" + itoa(next)
	}
	if got := strings.Join(all, "|"); got != "d5,d3|d4,d2|d1" {
		t.Fatalf("pages: %s", got)
	}
	if l := a.ok(a.alice, "GET", "/api/v1/dictations?app=Messages&limit=1", nil); refs(l) != "d3" || num(l, "next") == 0 {
		t.Fatalf("filtered page: %s %v", refs(l), l["next"])
	} else if l2 := a.ok(a.alice, "GET", "/api/v1/dictations?app=Messages&limit=1&before="+itoa(int64(num(l, "next"))), nil); refs(l2) != "d4" || num(l2, "next") != 0 {
		t.Fatalf("filtered second page: %s %v", refs(l2), l2["next"])
	}
}

func TestDictationStats(t *testing.T) {
	a := newApp(t, nil)
	post := func(auth, ref string, n int, app string, ms int64, at time.Time) {
		a.expect(http.StatusCreated, auth, "POST", "/api/v1/dictations", dict(ref, words(n), app, ms, at))
	}
	post(a.alice, "s1", 40, "Pocket Projects", 10000, onDay(0, 0))   // saves 60 - 10 = 50 s
	post(a.alice, "s2", 20, "Pocket Projects", 0, onDay(0, 0))       // 30 s
	post(a.alice, "s3", 10, "Pocket Projects", 60000, onDay(-1, 12)) // slower than typing: 0
	post(a.alice, "s4", 4, "Slack", 2000, onDay(-2, 12))             // 6 - 2 = 4 s
	post(a.alice, "s5", 2, "Slack", 1000, onDay(-4, 12))             // 3 - 1 = 2 s; the streak ends before it
	post(a.alice, "s6", 4, "", 0, onDay(-40, 12))                    // long ago, no app: 6 s
	st := a.ok(a.alice, "GET", "/api/v1/dictations/stats", nil)
	// 80 words in 73 s of speaking: 65.75 a minute
	want := map[string]float64{"words": 80, "sessions": 6, "duration_ms": 73000, "saved_seconds": 92, "streak": 3, "wpm": 66}
	for k, v := range want {
		if num(st, k) != v {
			t.Errorf("%s = %v, want %v", k, st[k], v)
		}
	}
	today, _ := st["today"].(map[string]any)
	if num(today, "words") != 60 || num(today, "sessions") != 2 || num(today, "saved_seconds") != 80 {
		t.Errorf("today: %v", today)
	}
	week, _ := st["week"].(map[string]any)
	if num(week, "words") != 76 || num(week, "sessions") != 5 || num(week, "saved_seconds") != 86 {
		t.Errorf("week: %v", week)
	}
	apps, _ := st["apps"].([]any)
	if len(apps) != 2 || apps[0].(map[string]any)["app"] != "Pocket Projects" || num(apps[0].(map[string]any), "sessions") != 3 || num(apps[0].(map[string]any), "words") != 70 ||
		apps[1].(map[string]any)["app"] != "Slack" || num(apps[1].(map[string]any), "sessions") != 2 {
		t.Errorf("apps: %v", apps)
	}
	days, _ := st["days"].([]any)
	if len(days) != 30 {
		t.Fatalf("days: %d", len(days))
	}
	last, first := days[29].(map[string]any), days[0].(map[string]any)
	if last["date"] != onDay(0, 12).Format("2006-01-02") || num(last, "words") != 60 || num(last, "sessions") != 2 {
		t.Errorf("today's bar: %v", last)
	}
	if first["date"] != onDay(-29, 12).Format("2006-01-02") || num(first, "sessions") != 0 {
		t.Errorf("first bar: %v", first)
	}
	if d := days[25].(map[string]any); num(d, "words") != 2 || d["date"] != onDay(-4, 12).Format("2006-01-02") {
		t.Errorf("four days ago: %v", d)
	}
	// nothing yet today: the streak still counts up to yesterday
	post(a.bob, "b1", 5, "Mail", 0, onDay(-1, 12))
	post(a.bob, "b2", 5, "Mail", 0, onDay(-2, 12))
	if st := a.ok(a.bob, "GET", "/api/v1/dictations/stats", nil); num(st, "streak") != 2 || num(st, "wpm") != 0 || num(num2(st, "today"), "sessions") != 0 {
		t.Errorf("Bob: %v", st)
	}
	// and the summary says how much was dictated today
	if s := a.ok(a.alice, "GET", "/api/v1/summary", nil); num(s, "dictation_words_today") != 60 || num(s, "dictation_sessions_today") != 2 {
		t.Errorf("summary: %v", s)
	}
}

func num2(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }

func TestDictationLinkScope(t *testing.T) {
	read := []string{"voice:read"}
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/dictations", true},
		{"GET", "/api/v1/dictations/stats", true},
		{"POST", "/api/v1/dictations", false},
		{"DELETE", "/api/v1/dictations", false},
		{"DELETE", "/api/v1/dictations/3", false},
	} {
		if got := linkAllows(read, httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("voice:read %s %s = %v", c.method, c.path, got)
		}
	}
}

func TestDictationLinkTranscribeMayAddHistory(t *testing.T) {
	tr := []string{"voice:transcribe"}
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"POST", "/api/v1/dictations", true}, // Pocket AI adds what was dictated in its composer
		{"GET", "/api/v1/dictations", false},
		{"DELETE", "/api/v1/dictations", false},
		{"DELETE", "/api/v1/dictations/3", false},
	} {
		if got := linkAllows(tr, httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("voice:transcribe %s %s = %v", c.method, c.path, got)
		}
	}
}

func TestDictationSource(t *testing.T) {
	a := newApp(t, nil)
	// Pocket Launcher sends no source
	mac := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", dict("src-mac", "from the mac", "Slack", 1000, time.Now().Add(-2*time.Minute)))
	if d := num2(mac, "dictation"); d["source"] != "Pocket Launcher" {
		t.Fatalf("no source: %v", d)
	}
	phone := dict("src-phone", "from the phone", "Pocket AI", 1500, time.Now().Add(-time.Minute))
	phone["source"], phone["device"], phone["app_id"] = "Pocket AI app", "iPhone", ""
	if d := num2(a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/dictations", phone), "dictation"); d["source"] != "Pocket AI app" || d["device"] != "iPhone" {
		t.Fatalf("phone: %v", d)
	}
	l := a.ok(a.alice, "GET", "/api/v1/dictations?source="+strings.ReplaceAll("Pocket AI app", " ", "%20"), nil)
	items, _ := l["items"].([]any)
	if num(l, "total") != 1 || len(items) != 1 || items[0].(map[string]any)["text"] != "from the phone" {
		t.Fatalf("filtered by source: %v", l)
	}
	st := a.ok(a.alice, "GET", "/api/v1/dictations/stats", nil)
	srcs, _ := st["sources"].([]any)
	if len(srcs) != 2 {
		t.Fatalf("stats sources: %v", st["sources"])
	}
	for _, c := range []struct{ claimed, app, want string }{
		{"", "", "Pocket Launcher"}, {"", "ai", "Pocket AI"}, {"", "day", "Pocket Day"}, {"Pocket AI on the web", "ai", "Pocket AI on the web"},
	} {
		if got := dictationSource(c.claimed, c.app); got != c.want {
			t.Errorf("dictationSource(%q, %q) = %q, want %q", c.claimed, c.app, got, c.want)
		}
	}
}
