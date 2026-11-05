package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- tidying up with AI ----
//
// After a memo is written out, the person's AI (Pocket AI through Core, or
// their own OpenAI key) gets the words — never the recording — and gives a
// title, a short summary, the words cleaned up (punctuation, paragraphs, no
// "um"), what the memo is about, and what in it belongs in another app: tasks
// with their day, an appointment, a line for today's note, money spent.
// Nothing is sent anywhere until the person presses the button.

const aiTidy = `You tidy up one person's voice memo, written out by speech recognition, and find what in it belongs in their other apps.

Rules:
- Write in the language of the memo (Bosnian, Croatian and Serbian in Latin letters). Never translate.
- "text": the memo cleaned up: correct punctuation and capitals, paragraphs where the topic changes, filler words and false starts removed, obvious recognition mistakes fixed. Keep every fact and the person's own words and voice; add nothing. A list they spoke becomes a list with "- " lines.
- "title": what the memo is about, at most 8 words, no full stop.
- "summary": one or two plain sentences; empty when the memo is shorter than two sentences.
- "kind": one of note, todo, idea, journal, meeting, shopping, reminder.
- "tags": up to 3 short lowercase topic words, or none.
- "actions": only what the person clearly asked of themselves or clearly mentioned; never invent. Each one of:
  {"type":"task","title":"what to do, starting with a verb","due":"YYYY-MM-DD or empty","notes":"short context or empty"}
  {"type":"event","title":"...","start":"YYYY-MM-DDTHH:MM, or YYYY-MM-DD when no time was said","end":"same format or empty","location":"or empty"}
  {"type":"journal","text":"one or two sentences in the first person for today's journal, only when the memo is about their day or feelings"}
  {"type":"expense","title":"what it was for","amount":12.5,"currency":"three-letter code, or empty when not said","date":"YYYY-MM-DD"}
  Shopping items become one task each ("Buy milk"), unless there are more than five: then one task "Shopping" with the items in notes.
  Work out dates from the moment the memo was recorded ("tomorrow", "next Tuesday", "on the 14th"). No actions is a fine answer.

Answer with JSON only: {"title":"","summary":"","text":"","kind":"","tags":[],"actions":[]}`

type tidyOut struct {
	Title   string         `json:"title"`
	Summary string         `json:"summary"`
	Text    string         `json:"text"`
	Kind    string         `json:"kind"`
	Tags    []string       `json:"tags"`
	Actions []store.Action `json:"actions"`
}

// tidySoon tidies a memo in the background (two at a time for the box).
func (s *Server) tidySoon(uid, id int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		if err := s.tidy(ctx, uid, id); err != nil {
			s.log.Debug("tidy", "memo", id, "err", err)
		}
	}()
}

// aiReady reports whether the person has an AI to tidy with.
func (s *Server) aiReady(ctx context.Context, uid int64, st store.Settings) bool {
	cl, _, _ := s.aiFor(ctx, uid, st)
	return cl != nil
}

// tidy runs the AI on one memo and saves what it found.
func (s *Server) tidy(ctx context.Context, uid, id int64) error {
	select {
	case s.aiSem <- struct{}{}:
		defer func() { <-s.aiSem }()
	case <-ctx.Done():
		return ctx.Err()
	}
	m, err := s.store.GetMemo(ctx, uid, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(m.Transcript) == "" {
		return fmt.Errorf("there are no words to tidy up yet")
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		if why == "" {
			why = "no AI is connected (Settings → AI)"
		}
		return fmt.Errorf("%s", why)
	}
	m.AIStatus, m.AIError = "working", ""
	_ = s.store.SaveMemo(ctx, uid, m)

	rec := time.Now()
	if t, err := time.Parse(time.RFC3339Nano, m.RecordedAt); err == nil {
		rec = t.Local()
	}
	text := m.Transcript
	if utf8.RuneCountInString(text) > 24000 {
		text = string([]rune(text)[:24000])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recorded: %s, %s (time zone %s).\n", rec.Format("Monday 2 January 2006"), rec.Format("15:04"), rec.Location().String())
	if m.Language != "" {
		if n := speech.LanguageName(m.Language); n != "" {
			fmt.Fprintf(&b, "Language heard: %s.\n", n)
		}
	}
	if st.Currency != "" {
		fmt.Fprintf(&b, "Their usual currency: %s.\n", st.Currency)
	}
	if v := strings.TrimSpace(st.Vocabulary); v != "" {
		fmt.Fprintf(&b, "Names and words they use: %s\n", excerpt(v, 400))
	}
	fmt.Fprintf(&b, "\nThe memo (%s):\n%s", clock(m.DurationMS), text)

	var out tidyOut
	err = cl.JSON(ctx, aiTidy, b.String(), &out)
	// read the memo again: the person may have changed it meanwhile
	cur, gerr := s.store.GetMemo(ctx, uid, id)
	if gerr != nil {
		return gerr
	}
	if err != nil {
		cur.AIStatus, cur.AIError = "failed", err.Error()
		_ = s.store.SaveMemo(ctx, uid, cur)
		return err
	}
	applyTidy(cur, out, rec)
	cur.AIStatus, cur.AIError, cur.AIProvider = "done", "", used
	if err := s.store.SaveMemo(ctx, uid, cur); err != nil {
		return err
	}
	s.log.Debug("memo tidied", "memo", id, "provider", used, "actions", len(cur.Actions))
	return nil
}

// applyTidy keeps what the person set themselves and the actions already sent.
func applyTidy(m *store.Memo, out tidyOut, rec time.Time) {
	if t := cleanLine(out.Title, 80); t != "" && !m.TitleSet {
		m.Title = strings.TrimRight(t, ".")
	}
	m.Summary = strings.TrimSpace(out.Summary)
	m.Tidy = strings.TrimSpace(out.Text)
	if k := strings.ToLower(strings.TrimSpace(out.Kind)); store.ValidKind(k) && !m.KindSet {
		m.Kind = k
	}
	if strings.TrimSpace(m.Tags) == "" && len(out.Tags) > 0 {
		if len(out.Tags) > 3 {
			out.Tags = out.Tags[:3]
		}
		m.Tags = store.JoinTags(strings.Join(out.Tags, ","))
	}
	keep := []store.Action{}
	for _, a := range m.Actions {
		if a.SentTo != "" {
			keep = append(keep, a)
		}
	}
	counts := map[string]int{}
	limit := map[string]int{"task": 12, "event": 3, "journal": 1, "expense": 4}
	for _, a := range out.Actions {
		a.Type = strings.ToLower(strings.TrimSpace(a.Type))
		if counts[a.Type] >= limit[a.Type] {
			continue
		}
		if !cleanAction(&a, rec) || sameAction(keep, a) {
			continue
		}
		counts[a.Type]++
		keep = append(keep, a)
	}
	m.Actions = keep
}

func sameAction(list []store.Action, a store.Action) bool {
	for _, x := range list {
		if x.Type == a.Type && strings.EqualFold(x.Title, a.Title) && x.Text == a.Text {
			return true
		}
	}
	return false
}

func cleanLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}

// cleanAction checks one suggestion; false = drop it.
func cleanAction(a *store.Action, rec time.Time) bool {
	a.SentTo, a.SentRef, a.SentURL, a.Skipped = "", "", "", false
	a.Title, a.Notes, a.Location = cleanLine(a.Title, 160), strings.TrimSpace(a.Notes), cleanLine(a.Location, 160)
	day := func(v string) string {
		v = strings.TrimSpace(v)
		if len(v) >= 10 && validDate(v[:10]) {
			return v[:10]
		}
		return ""
	}
	switch a.Type {
	case "task":
		a.Due = day(a.Due)
		return a.Title != ""
	case "event":
		st := strings.TrimSpace(a.Start)
		switch {
		case len(st) >= 16 && validDateTime(st[:16]):
			a.Start = st[:16]
		case day(st) != "":
			a.Start = day(st)
		default:
			return false
		}
		en := strings.TrimSpace(a.End)
		if len(a.Start) == 16 && len(en) >= 16 && validDateTime(en[:16]) && en[:16] > a.Start {
			a.End = en[:16]
		} else if len(a.Start) == 10 && day(en) != "" && day(en) >= a.Start {
			a.End = day(en)
		} else {
			a.End = ""
		}
		return a.Title != ""
	case "journal":
		a.Text = strings.TrimSpace(a.Text)
		a.Title = ""
		return a.Text != ""
	case "expense":
		if a.Amount <= 0 || a.Amount > 1e7 || math.IsNaN(a.Amount) {
			return false
		}
		a.Amount = math.Round(a.Amount*100) / 100
		a.Currency = cleanCurrency(a.Currency)
		a.Date = day(a.Date)
		if a.Date == "" {
			a.Date = rec.Format("2006-01-02")
		}
		return a.Title != ""
	}
	return false
}

func validDateTime(v string) bool {
	_, err := time.Parse("2006-01-02T15:04", v)
	return err == nil
}

// clock is a duration as m:ss.
func clock(ms int64) string {
	sec := (ms + 500) / 1000
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec%3600/60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// handleTidy runs the AI on a memo now and returns it.
func (s *Server) handleTidy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id := idParam(r, "id")
	if _, err := s.store.GetMemo(ctx, uid, id); err != nil {
		s.fail(w, err, "memo")
		return
	}
	if err := s.tidy(ctx, uid, id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := s.store.GetMemo(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}
