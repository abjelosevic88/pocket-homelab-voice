package server

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// What Pocket Search, Pocket Day and Pocket Hub ask every app (capabilities
// in links.go; docs/capabilities.md in pocket-homelab-core). Each runs as the
// request's person and only reads what is stored, with the queries of the
// app's own search, day and summary: nothing is written out, tidied by the
// AI, sent on or removed from here.

func memoPath(id int64) string { return "/memos/" + strconv.FormatInt(id, 10) }

// isMeeting: a call recorded as a meeting, or a memo the person or the AI
// filed under Meetings.
func isMeeting(m *store.Memo) bool { return m.Meeting || m.Kind == "meeting" }

// memoKind is "meeting" or "memo".
func memoKind(m *store.Memo) string {
	if isMeeting(m) {
		return "meeting"
	}
	return "memo"
}

// memoTitle is the memo's title, or what the app's pages show without one.
func memoTitle(m *store.Memo) string {
	if t := strings.TrimSpace(m.Title); t != "" {
		return t
	}
	if isMeeting(m) {
		return "Meeting"
	}
	return "Memo"
}

// countOf writes "1 memo" or "3 memos".
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// spokenLength is how long a recording is, as the app says it in summaries:
// "45 s", "6 min", "1 h 5 min"; "" for a typed memo.
func spokenLength(ms int64) string {
	if ms <= 0 {
		return ""
	}
	s := (ms + 500) / 1000
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	m := (s + 30) / 60
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%d h %d min", m/60, m%60)
}

// recordedAt is when a memo was recorded (zero when the stored time is unreadable).
func recordedAt(m *store.Memo) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, m.RecordedAt)
	return t
}

// shortDate is "12 Nov" this year, "12 Nov 2025" before.
func shortDate(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(now.Location())
	if t.Year() == now.Year() {
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// whenWord says when, seen from now: "today 09:30", "yesterday 18:10",
// "Monday" within a week, then "12 Nov" and "12 Nov 2025".
func whenWord(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(now.Location())
	midnight := func(x time.Time) time.Time {
		return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, now.Location())
	}
	days := int(math.Round(midnight(now).Sub(midnight(t)).Hours() / 24))
	switch {
	case days <= 0:
		return "today " + t.Format("15:04")
	case days == 1:
		return "yesterday " + t.Format("15:04")
	case days < 7:
		return t.Format("Monday")
	}
	return shortDate(t, now)
}

// joinDot joins the parts that are there with " · ".
func joinDot(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// whoSpoke names the people heard in a meeting, as its page shows them once
// they are named or recognised: "with Alex and Mia". The person themself and
// voices nobody named are left out.
func whoSpoke(m *store.Memo) string {
	var names []string
	for _, sp := range m.Speakers {
		if sp.Key == speakerYou || sp.State == store.SpeakerYou || strings.TrimSpace(sp.Name) == "" {
			continue
		}
		names = append(names, strings.TrimSpace(sp.Name))
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "with " + names[0]
	}
	return "with " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// ---- search ----

// hasWord: the words have at least one letter or digit to look for.
func hasWord(q string) bool {
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// matchPart is the stretch of text around the first word of the search it
// holds, a few words either side, plain; "" when none of the words is in it.
func matchPart(text string, words []string) string {
	ws := strings.Fields(text)
	for i, w := range ws {
		f := speech.Fold(w)
		for _, q := range words {
			if !strings.Contains(f, q) {
				continue
			}
			from, to := max(0, i-6), min(len(ws), i+14)
			part := strings.Join(ws[from:to], " ")
			if from > 0 {
				part = "…" + part
			}
			if to < len(ws) {
				part += "…"
			}
			return part
		}
	}
	return ""
}

// memoSnippet is the part of a memo's summary, tidy text or words that
// matched; the summary or the start of the words when only the title did.
func memoSnippet(m *store.Memo, words []string) string {
	for _, t := range []string{m.Summary, m.Tidy, m.Transcript} {
		if p := matchPart(t, words); p != "" {
			return p
		}
	}
	return excerpt(firstNonEmpty(m.Summary, m.Tidy, m.Transcript), 140)
}

// searchMemos is how many matching memos are read at least, to rank.
const searchMemos = 20

// searchWords are the words of a search as the store looks for them.
func searchWords(q string) []string { return strings.Fields(speech.Fold(q)) }

// pocketSearch is the app's own search (GET /search): memos and meetings by
// title, summary, tidy text, words and tags, and what was dictated, by its
// text. A match in the title comes first, then other memos, then
// dictations, newest first within each.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	if !hasWord(q) {
		return nil, nil
	}
	ctx, uid := r.Context(), actorID(r.Context())
	words := searchWords(q)
	// a few more memos than asked for, so an older match in the title still leads
	ms, _, err := s.store.ListMemos(ctx, uid, store.MemoFilter{Q: q, Limit: max(limit, searchMemos)})
	if err != nil {
		return nil, err
	}
	ds, _, _, err := s.store.ListDictations(ctx, uid, store.DictationFilter{Q: q, Limit: limit})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	type hit struct {
		item core.SearchItem
		rank int
	}
	var hits []hit
	for i := range ms {
		m := &ms[i]
		at := recordedAt(m)
		rank := 2
		if title := speech.Fold(m.Title); title != "" {
			all := true
			for _, w := range words {
				all = all && strings.Contains(title, w)
			}
			if all {
				rank = 3
			}
		}
		date := ""
		if !at.IsZero() {
			date = at.UTC().Format(time.RFC3339)
		}
		hits = append(hits, hit{rank: rank, item: core.SearchItem{
			ID: "memo:" + strconv.FormatInt(m.ID, 10), Title: memoTitle(m), Kind: memoKind(m),
			Snippet: joinDot(memoSnippet(m, words), spokenLength(m.DurationMS), shortDate(at, now)),
			Date:    date, URL: memoPath(m.ID),
		}})
	}
	for _, d := range ds {
		at, _ := time.Parse(time.RFC3339Nano, d.DictatedAt)
		date, into := "", ""
		if !at.IsZero() {
			date = at.UTC().Format(time.RFC3339)
		}
		if d.App != "" {
			into = "into " + d.App
		}
		part := matchPart(d.Text, words)
		if part == "" {
			part = excerpt(d.Text, 140)
		}
		hits = append(hits, hit{rank: 1, item: core.SearchItem{
			ID: "dictation:" + strconv.FormatInt(d.ID, 10), Title: excerpt(d.Text, 80), Kind: "dictation",
			Snippet: joinDot(part, into, spokenLength(d.DurationMS), shortDate(at, now)),
			Date:    date,
			// the Dictation page, searched for the same words, shows it
			URL: "/dictation?" + url.Values{"q": {q}}.Encode(),
		}})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].rank > hits[j].rank })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	items := make([]core.SearchItem, 0, len(hits))
	for i, h := range hits {
		// higher first, in the order above
		h.item.Score = float64(h.rank) + 1 - float64(i+1)/float64(len(hits)+1)
		items = append(items, h.item)
	}
	return items, nil
}

// ---- day ----

// dayMemos is how many memos of a day are read at most (what the answer may hold).
const dayMemos = 200

// pocketDay is the app's own day (GET /day/{date}) in the common shape: the
// memos and meetings recorded that day and written out, oldest first, and
// how much was dictated as numbers. The day is the caller's (tz), else this
// server's. Memos still being written out, or that could not be, wait for
// the next reading, as in the journal Pocket Day has kept so far.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	if tz == nil {
		tz = time.Local
	}
	start, err := time.ParseInLocation("2006-01-02", date, tz)
	if err != nil {
		return nil, err
	}
	end := start.AddDate(0, 0, 1)
	ms, _, err := s.store.ListMemos(ctx, uid, store.MemoFilter{Since: start, Until: end, Status: store.StatusDone, Oldest: true, Limit: dayMemos})
	if err != nil {
		return nil, err
	}
	words, dictations := s.store.DictationsBetween(ctx, uid, start, end)
	if len(ms) == 0 && dictations == 0 {
		return nil, nil
	}
	folders := map[int64]string{}
	if fs, err := s.store.ListFolders(ctx, uid); err == nil {
		for _, f := range fs {
			folders[f.ID] = f.Name
		}
	}

	day := &core.Day{Date: date, Items: []core.DayItem{}}
	var memos, meetings int
	var meetingMS int64
	for i := range ms {
		m := &ms[i]
		at := ""
		if t := recordedAt(m); !t.IsZero() {
			at = t.In(tz).Format("15:04")
		}
		if isMeeting(m) {
			meetings++
			meetingMS += m.DurationMS
		} else {
			memos++
		}
		day.Items = append(day.Items, core.DayItem{
			ID: strconv.FormatInt(m.ID, 10), Time: at, Title: memoTitle(m), Kind: memoKind(m),
			Sub:     joinDot(folders[m.FolderID], whoSpoke(m)),
			Minutes: math.Round(float64(m.DurationMS)/6000) / 10,
			Note:    strings.TrimSpace(m.Summary), URL: memoPath(m.ID),
		})
	}
	if dictations > 0 {
		day.Metrics = []core.Metric{{Label: "Dictations", Value: strconv.FormatInt(dictations, 10)}, {Label: "Words dictated", Value: strconv.FormatInt(words, 10)}}
	}
	day.Summary = voiceDaySummary(memos, meetings, meetingMS, int(dictations))
	return day, nil
}

// voiceDaySummary says a day in a line: "2 memos, 1 meeting (45 min)"; the
// dictations only on a day with nothing recorded ("3 dictations").
func voiceDaySummary(memos, meetings int, meetingMS int64, dictations int) string {
	var parts []string
	if memos > 0 {
		parts = append(parts, countOf(memos, "memo", "memos"))
	}
	if meetings > 0 {
		p := countOf(meetings, "meeting", "meetings")
		if l := spokenLength(meetingMS); l != "" {
			p += " (" + l + ")"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 && dictations > 0 {
		parts = append(parts, countOf(dictations, "dictation", "dictations"))
	}
	return strings.Join(parts, ", ")
}

// ---- widget ----

// widgetRows is how many memos the widget shows.
const widgetRows = 6

// pocketWidget is the newest memos and meetings: the ones being written out
// first, then those with suggestions still to send on, then the rest; and
// how many were recorded today. It reads the newest few rows only.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx, uid := r.Context(), actorID(r.Context())
	c := s.store.CountMemos(ctx, uid)
	if c.All == 0 {
		return nil, nil
	}
	var busy []store.Memo
	if c.Working > 0 {
		for _, st := range []string{store.StatusWorking, store.StatusQueued} {
			ms, _, err := s.store.ListMemos(ctx, uid, store.MemoFilter{Status: st, Limit: widgetRows})
			if err != nil {
				return nil, err
			}
			busy = append(busy, ms...)
		}
	}
	newest, _, err := s.store.ListMemos(ctx, uid, store.MemoFilter{Limit: widgetRows})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	_, today, err := s.store.ListMemos(ctx, uid, store.MemoFilter{Since: midnight, Until: midnight.AddDate(0, 0, 1), Limit: 1})
	if err != nil {
		return nil, err
	}

	var waiting, rest []store.Memo
	for _, m := range newest {
		if m.Status == store.StatusQueued || m.Status == store.StatusWorking {
			continue // among busy already
		}
		if openActions(m) > 0 {
			waiting = append(waiting, m)
		} else {
			rest = append(rest, m)
		}
	}
	out := &core.WidgetData{URL: "/"}
	seen := map[int64]bool{}
	for _, m := range append(append(busy, waiting...), rest...) {
		if seen[m.ID] || len(out.Items) == widgetRows {
			continue
		}
		seen[m.ID] = true
		out.Items = append(out.Items, core.WidgetItem{ID: strconv.FormatInt(m.ID, 10), Title: memoTitle(&m), Detail: widgetDetail(&m, now), URL: memoPath(m.ID)})
	}
	var line []string
	if today > 0 {
		line = append(line, countOf(today, "memo", "memos")+" today")
	}
	if c.Working > 0 {
		line = append(line, strconv.Itoa(c.Working)+" being written out")
	}
	out.Line = strings.Join(line, ", ")
	return out, nil
}

// widgetDetail is a memo's length and when it was recorded, with what is
// still to happen to it: "3 min · today 09:30 · 2 to send on".
func widgetDetail(m *store.Memo, now time.Time) string {
	state := ""
	switch m.Status {
	case store.StatusQueued:
		state = "waiting to be written out"
	case store.StatusWorking:
		state = "being written out"
	case store.StatusFailed:
		state = "could not be written out"
	default:
		if n := openActions(*m); n > 0 {
			state = strconv.Itoa(n) + " to send on"
		}
	}
	return joinDot(spokenLength(m.DurationMS), whenWord(recordedAt(m), now), state)
}
