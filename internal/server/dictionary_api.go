package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- the dictionary ----

func (s *Server) handleListWords(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := s.store.Words(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "dictionary")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// cleanWord checks a phrase and a replacement; "" = fine, else why not.
func cleanWord(phrase, with string) (string, string, string) {
	phrase = strings.Join(strings.Fields(phrase), " ")
	with = strings.TrimSpace(with)
	if n := utf8.RuneCountInString(phrase); n == 0 || n > 100 {
		return "", "", "phrase is 1 to 100 characters"
	}
	if utf8.RuneCountInString(with) > 200 {
		return "", "", "replace_with is at most 200 characters"
	}
	return phrase, with, ""
}

func (s *Server) handleAddWord(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Phrase      string  `json:"phrase"`
		ReplaceWith *string `json:"replace_with"`
		Source      string  `json:"source"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	with := ""
	if in.ReplaceWith != nil {
		with = *in.ReplaceWith
	}
	phrase, with, why := cleanWord(in.Phrase, with)
	if why != "" {
		writeErr(w, http.StatusBadRequest, why)
		return
	}
	source := strings.ToLower(strings.TrimSpace(in.Source))
	if source == "" {
		source = "manual"
	}
	if source != "manual" && source != "learned" {
		writeErr(w, http.StatusBadRequest, "source is manual or learned")
		return
	}
	// the phrase is there already (whatever its capitals): give it back, with the new replacement
	if have, err := s.store.WordByPhrase(ctx, uid, phrase); err == nil {
		if in.ReplaceWith != nil && have.ReplaceWith != with {
			have.ReplaceWith = with
			if err := s.store.SaveWord(ctx, uid, have); err != nil {
				s.fail(w, err, "dictionary")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"item": have})
		return
	}
	item := &store.Word{Phrase: phrase, ReplaceWith: with, Source: source}
	if err := s.store.AddWord(ctx, uid, item); err != nil {
		if errors.Is(err, store.ErrConflict) { // two at once
			if have, gerr := s.store.WordByPhrase(ctx, uid, phrase); gerr == nil {
				writeJSON(w, http.StatusOK, map[string]any{"item": have})
				return
			}
		}
		s.fail(w, err, "dictionary")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": item})
}

func (s *Server) handleUpdateWord(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	item, err := s.store.GetWord(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "dictionary")
		return
	}
	var in struct {
		Phrase      *string `json:"phrase"`
		ReplaceWith *string `json:"replace_with"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	phrase, with := item.Phrase, item.ReplaceWith
	if in.Phrase != nil {
		phrase = *in.Phrase
	}
	if in.ReplaceWith != nil {
		with = *in.ReplaceWith
	}
	phrase, with, why := cleanWord(phrase, with)
	if why != "" {
		writeErr(w, http.StatusBadRequest, why)
		return
	}
	item.Phrase, item.ReplaceWith = phrase, with
	if err := s.store.SaveWord(ctx, uid, item); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "that phrase is in the dictionary already")
			return
		}
		s.fail(w, err, "dictionary")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (s *Server) handleDeleteWord(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteWord(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "dictionary")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- dictation styles ----

func (s *Server) handleGetStyles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeJSON(w, http.StatusOK, s.store.GetDictationStyles(ctx, actorID(ctx)))
}

func (s *Server) handlePutStyles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in store.DictationStyles
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Default = strings.ToLower(strings.TrimSpace(in.Default))
	if in.Default == "" {
		in.Default = store.StyleNatural
	}
	if !store.ValidStyle(in.Default) {
		writeErr(w, http.StatusBadRequest, "a style is one of "+strings.Join(store.Styles, ", "))
		return
	}
	if len(in.Apps) > 200 {
		writeErr(w, http.StatusBadRequest, "at most 200 apps")
		return
	}
	out := store.DictationStyles{Default: in.Default, Apps: []store.AppStyle{}}
	seen := map[string]bool{}
	for _, a := range in.Apps {
		a.AppID, a.App, a.Style = clip(a.AppID, 255), clip(a.App, 200), strings.ToLower(strings.TrimSpace(a.Style))
		if a.AppID == "" && a.App == "" {
			writeErr(w, http.StatusBadRequest, "each app needs an app_id or an app name")
			return
		}
		if !store.ValidStyle(a.Style) {
			writeErr(w, http.StatusBadRequest, "a style is one of "+strings.Join(store.Styles, ", "))
			return
		}
		key := strings.ToLower(firstNonEmpty(a.AppID, "name:"+a.App))
		if seen[key] {
			continue // the first one counts
		}
		seen[key] = true
		out.Apps = append(out.Apps, a)
	}
	if err := s.store.SaveDictationStyles(ctx, actorID(ctx), out); err != nil {
		s.fail(w, err, "dictation styles")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
