package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// Dictation history: Pocket Launcher posts every finished dictation here
// (from a retrying outbox, so the client's ref makes a POST idempotent), and
// the Dictation page lists them with what they add up to.

const (
	maxDictationRef  = 64
	maxDictationText = 20000
)

// clip trims a string and cuts it to at most n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (s *Server) handleCreateDictation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Ref        string `json:"ref"`
		Text       string `json:"text"`
		Raw        string `json:"raw"`
		App        string `json:"app"`
		AppID      string `json:"app_id"`
		DurationMS int64  `json:"duration_ms"`
		Engine     string `json:"engine"`
		Language   string `json:"language"`
		Device     string `json:"device"`
		Polished   bool   `json:"polished"`
		DictatedAt string `json:"dictated_at"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" || utf8.RuneCountInString(ref) > maxDictationRef {
		writeErr(w, http.StatusBadRequest, "ref is required (1–64 characters)")
		return
	}
	// a retry of one that is already here: give it back as it was stored
	if d, err := s.store.DictationByRef(ctx, uid, ref); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"dictation": d})
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	if utf8.RuneCountInString(text) > maxDictationText {
		writeErr(w, http.StatusBadRequest, "text is longer than 20000 characters")
		return
	}
	if in.DurationMS < 0 {
		writeErr(w, http.StatusBadRequest, "duration_ms must not be negative")
		return
	}
	at := time.Now()
	if v := strings.TrimSpace(in.DictatedAt); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "dictated_at must be RFC 3339 with an offset")
			return
		}
		if t.After(time.Now().Add(5 * time.Minute)) {
			writeErr(w, http.StatusBadRequest, "dictated_at is in the future")
			return
		}
		at = t
	}
	d := &store.Dictation{
		Ref: ref, Text: text, Raw: clip(in.Raw, maxDictationText), App: clip(in.App, 200), AppID: clip(in.AppID, 255),
		DurationMS: in.DurationMS, Engine: clip(in.Engine, 100), Language: clip(in.Language, 20), Device: clip(in.Device, 200),
		Polished: in.Polished, DictatedAt: store.DictationTime(at),
	}
	created, err := s.store.CreateDictation(ctx, uid, d)
	if err != nil {
		s.fail(w, err, "save dictation")
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, map[string]any{"dictation": d})
}

func (s *Server) handleListDictations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	f := store.DictationFilter{Q: q.Get("q"), App: q.Get("app"), From: q.Get("from"), To: q.Get("to"), Limit: queryInt(r, "limit")}
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	if !validDate(f.From) || !validDate(f.To) {
		writeErr(w, http.StatusBadRequest, "from and to are days: YYYY-MM-DD")
		return
	}
	items, total, next, err := s.store.ListDictations(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "dictations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "next": next})
}

func (s *Server) handleDictationStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.store.DictationStatsFor(ctx, actorID(ctx), time.Now())
	if err != nil {
		s.fail(w, err, "dictation stats")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleDeleteDictation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteDictation(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete dictation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleClearDictations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n, err := s.store.ClearDictations(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "clear dictations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}
