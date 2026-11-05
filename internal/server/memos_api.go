package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// memoView is a memo as the API gives it: where it opens, where its audio
// is, and how far it is when it waits to be written out.
type memoView struct {
	store.Memo
	URL      string    `json:"url"`
	AudioURL string    `json:"audio_url"`
	Progress *progress `json:"progress,omitempty"`
	Queue    int       `json:"queue,omitempty"`
}

func (s *Server) memoURL(id int64) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/memos/" + strconv.FormatInt(id, 10)
}

func (s *Server) memoOut(ctx context.Context, m *store.Memo) memoView {
	v := memoView{Memo: *m, URL: s.memoURL(m.ID)}
	if m.HasAudio {
		v.AudioURL = fmt.Sprintf("/api/v1/memos/%d/audio", m.ID)
	}
	if m.Status == store.StatusQueued || m.Status == store.StatusWorking {
		if p, ok := s.progressOf(m.ID); ok {
			v.Progress = &p
		}
		if m.Status == store.StatusQueued {
			v.Queue = s.store.QueuePosition(ctx, m.ID)
		}
	}
	return v
}

func (s *Server) memoList(ctx context.Context, ms []store.Memo) []memoView {
	out := make([]memoView, 0, len(ms))
	for i := range ms {
		out = append(out, s.memoOut(ctx, &ms[i]))
	}
	return out
}

func memoFilter(r *http.Request) store.MemoFilter {
	q := r.URL.Query()
	f := store.MemoFilter{Q: q.Get("q"), Kind: q.Get("kind"), Tag: q.Get("tag"), Inbox: queryBool(r, "inbox"), Starred: queryBool(r, "starred"),
		Status: q.Get("status"), From: q.Get("from"), To: q.Get("to"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset"), Oldest: queryBool(r, "oldest")}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if !validDate(f.From) {
		f.From = ""
	}
	if !validDate(f.To) {
		f.To = ""
	}
	return f
}

func (s *Server) handleListMemos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	ms, total, err := s.store.ListMemos(ctx, uid, memoFilter(r))
	if err != nil {
		s.fail(w, err, "memos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.memoList(ctx, ms), "total": total, "counts": s.store.CountMemos(ctx, uid)})
}

func (s *Server) handleGetMemo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	m, err := s.store.GetMemo(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}

// audio types Voice takes, by extension (browsers send odd types for some).
var audioExt = map[string]string{
	".webm": "audio/webm", ".weba": "audio/webm", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg", ".mp3": "audio/mpeg",
	".m4a": "audio/mp4", ".mp4": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav", ".flac": "audio/flac", ".amr": "audio/amr",
	".3gp": "audio/3gpp", ".caf": "audio/x-caf", ".aiff": "audio/aiff", ".aif": "audio/aiff", ".wma": "audio/x-ms-wma", ".mov": "video/quicktime",
}

func extFor(name, mimeType string) (string, string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	if t, ok := audioExt[ext]; ok {
		if mimeType == "" || mimeType == "application/octet-stream" || !(strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "video/")) {
			mimeType = t
		}
		return ext, mimeType, true
	}
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	for e, t := range audioExt {
		if t == base {
			return e, mimeType, true
		}
	}
	if strings.HasPrefix(base, "audio/") || strings.HasPrefix(base, "video/") {
		return ".bin", mimeType, true
	}
	return "", "", false
}

func randName(ext string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%d-%s%s", time.Now().Unix(), hex.EncodeToString(b), ext)
}

// handleCreateMemo takes a recording (multipart "file") or a dictated text
// (JSON {"text"}) and queues it to be written out.
func (s *Server) handleCreateMemo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	app := linkApp(ctx)
	m := &store.Memo{Status: store.StatusQueued}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "application/json" {
		var in struct {
			Text       string `json:"text"`
			Title      string `json:"title"`
			Kind       string `json:"kind"`
			Tags       string `json:"tags"`
			RecordedAt string `json:"recorded_at"`
			Language   string `json:"language"`
			SourceRef  string `json:"source_ref"`
		}
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" {
			writeErr(w, http.StatusBadRequest, "send the words as text, or a recording as multipart file")
			return
		}
		m.Transcript, m.Status, m.Engine = in.Text, store.StatusDone, "typed"
		m.Segments = []store.Segment{{Start: 0, End: 0, Text: in.Text}}
		if !s.memoFields(w, m, in.Title, in.Kind, in.Tags, in.RecordedAt, in.Language) {
			return
		}
		m.SourceRef = strings.TrimSpace(in.SourceRef)
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxAudioBytes)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, "send the recording as multipart form field \"file\" (at most "+strconv.FormatInt(s.cfg.MaxAudioBytes>>20, 10)+" MB)")
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, fh, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "no recording in field \"file\"")
			return
		}
		defer f.Close()
		ext, typ, ok := extFor(fh.Filename, fh.Header.Get("Content-Type"))
		if !ok {
			writeErr(w, http.StatusBadRequest, "that is not a recording Voice can read (try m4a, mp3, wav, webm or ogg)")
			return
		}
		fv := r.FormValue
		if !s.memoFields(w, m, fv("title"), fv("kind"), fv("tags"), fv("recorded_at"), fv("language")) {
			return
		}
		if d, err := strconv.ParseInt(fv("duration_ms"), 10, 64); err == nil && d > 0 {
			m.DurationMS = d
		}
		m.SourceRef = strings.TrimSpace(fv("source_ref"))
		if app == "" && oneOf(fv("from"), []string{"upload", "import"}) {
			m.Source = fv("from")
			if m.Title == "" {
				m.Title = titleFromFile(fh.Filename)
			}
		}
		if err := os.MkdirAll(s.audioDir(uid), 0o755); err != nil {
			s.fail(w, err, "save recording")
			return
		}
		name := randName(ext)
		dst, err := os.Create(s.audioPath(uid, name))
		if err != nil {
			s.fail(w, err, "save recording")
			return
		}
		n, err := io.Copy(dst, f)
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil || n == 0 {
			_ = os.Remove(s.audioPath(uid, name))
			writeErr(w, http.StatusBadRequest, "the recording is empty or did not arrive whole")
			return
		}
		m.Audio, m.AudioType, m.AudioBytes = name, typ, n
	}
	if app != "" {
		m.Source = app
	}
	if m.Language == "" {
		st, _ := s.store.GetSettings(ctx, uid)
		m.Language = st.Language
	}
	if m.SourceRef != "" {
		if prev, err := s.store.MemoBySource(ctx, uid, m.Source, m.SourceRef); err == nil {
			if m.Audio != "" {
				_ = os.Remove(s.audioPath(uid, m.Audio))
			}
			writeJSON(w, http.StatusOK, s.memoOut(ctx, prev))
			return
		}
	}
	if m.Status == store.StatusDone && m.Title == "" {
		m.Title = firstWords(m.Transcript, 7)
	}
	if err := s.store.CreateMemo(ctx, uid, m); err != nil {
		if m.Audio != "" {
			_ = os.Remove(s.audioPath(uid, m.Audio))
		}
		s.fail(w, err, "save memo")
		return
	}
	s.publishMemo(ctx, m, "voice.memo_recorded")
	if m.Status == store.StatusQueued {
		s.enqueue()
	} else if st, _ := s.store.GetSettings(ctx, uid); st.AITidy {
		s.tidySoon(uid, m.ID)
	}
	writeJSON(w, http.StatusCreated, s.memoOut(ctx, m))
}

// memoFields checks the optional fields of a new memo.
func (s *Server) memoFields(w http.ResponseWriter, m *store.Memo, title, kind, tags, recorded, lang string) bool {
	m.Title = cleanLine(title, 140)
	m.TitleSet = m.Title != ""
	kind = strings.ToLower(strings.TrimSpace(kind))
	if !store.ValidKind(kind) {
		writeErr(w, http.StatusBadRequest, "kind is one of "+strings.Join(store.Kinds, ", "))
		return false
	}
	m.Kind, m.KindSet = kind, kind != ""
	m.Tags = store.JoinTags(tags)
	if recorded = strings.TrimSpace(recorded); recorded != "" {
		t, err := time.Parse(time.RFC3339, recorded)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "recorded_at is an RFC 3339 time like 2026-11-05T09:30:00+01:00")
			return false
		}
		if t.After(time.Now().Add(24 * time.Hour)) {
			t = time.Now()
		}
		m.RecordedAt = t.UTC().Format(time.RFC3339Nano)
	}
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "auto" {
		lang = ""
	}
	if !speech.ValidLanguage(lang) {
		writeErr(w, http.StatusBadRequest, "language is a two-letter code like bs or en")
		return false
	}
	m.Language = lang
	return true
}

// titleFromFile is a title from an imported file's name: "Shopping list.m4a"
// → "Shopping list"; Apple's "20240105 101500-…" or "New Recording 3" give none.
func titleFromFile(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	base = strings.TrimSpace(strings.NewReplacer("_", " ").Replace(base))
	if base == "" || strings.HasPrefix(strings.ToLower(base), "new recording") || strings.HasPrefix(strings.ToLower(base), "recording") || strings.HasPrefix(base, "memo-") {
		return ""
	}
	digits := 0
	for _, r := range base {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits*2 > len(base) {
		return ""
	}
	return cleanLine(base, 140)
}

// own reports whether the caller may change this memo: always the person,
// a linked app only for the memos it added.
func own(ctx context.Context, m *store.Memo) bool {
	app := linkApp(ctx)
	return app == "" || m.Source == app
}

func (s *Server) handleUpdateMemo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	if !own(ctx, m) {
		writeErr(w, http.StatusForbidden, "an app may change only the memos it added")
		return
	}
	var in struct {
		Title      string `json:"title"`
		Transcript string `json:"transcript"`
		Tidy       string `json:"tidy"`
		Summary    string `json:"summary"`
		Kind       string `json:"kind"`
		Tags       string `json:"tags"`
		Starred    bool   `json:"starred"`
		Filed      bool   `json:"filed"`
		RecordedAt string `json:"recorded_at"`
	}
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] {
		m.Title = cleanLine(in.Title, 140)
		m.TitleSet = m.Title != ""
		if m.Title == "" {
			m.Title = firstWords(m.Transcript, 7)
		}
	}
	if keys["transcript"] && strings.TrimSpace(in.Transcript) != m.Transcript {
		m.Transcript, m.Edited = strings.TrimSpace(in.Transcript), true
	}
	if keys["tidy"] {
		m.Tidy = strings.TrimSpace(in.Tidy)
	}
	if keys["summary"] {
		m.Summary = strings.TrimSpace(in.Summary)
	}
	if keys["kind"] {
		k := strings.ToLower(strings.TrimSpace(in.Kind))
		if !store.ValidKind(k) {
			writeErr(w, http.StatusBadRequest, "kind is one of "+strings.Join(store.Kinds, ", "))
			return
		}
		m.Kind, m.KindSet = k, k != ""
	}
	if keys["tags"] {
		m.Tags = store.JoinTags(in.Tags)
	}
	if keys["starred"] {
		m.Starred = in.Starred
	}
	if keys["filed"] {
		m.Filed = in.Filed
	}
	if keys["recorded_at"] {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(in.RecordedAt))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "recorded_at is an RFC 3339 time")
			return
		}
		m.RecordedAt = t.UTC().Format(time.RFC3339Nano)
	}
	if err := s.store.SaveMemo(ctx, uid, m); err != nil {
		s.fail(w, err, "save memo")
		return
	}
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}

func (s *Server) handleDeleteMemo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	if !own(ctx, m) {
		writeErr(w, http.StatusForbidden, "an app may remove only the memos it added")
		return
	}
	audio, err := s.store.DeleteMemo(ctx, uid, m.ID)
	if err != nil {
		s.fail(w, err, "delete memo")
		return
	}
	if audio != "" {
		_ = os.Remove(s.audioPath(uid, audio))
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAudio plays (or with ?download=1 saves) a memo's recording.
func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	if m.Audio == "" {
		writeErr(w, http.StatusNotFound, "this memo has no recording")
		return
	}
	f, err := os.Open(s.audioPath(uid, m.Audio))
	if err != nil {
		writeErr(w, http.StatusNotFound, "the recording file is missing")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", firstNonEmpty(m.AudioType, "application/octet-stream"))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	name := firstNonEmpty(safeFileName(m.Title), "memo") + filepath.Ext(m.Audio)
	disp := "inline"
	if queryBool(r, "download") {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func safeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return -1
		}
		return r
	}, s)
	return cleanLine(s, 80)
}

// handleRetry writes a memo out again (another language, a new engine).
func (s *Server) handleRetry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	var in struct {
		Language *string `json:"language"`
	}
	_ = decode(r, &in)
	if m.Audio == "" {
		writeErr(w, http.StatusBadRequest, "this memo has no recording to write out again")
		return
	}
	if m.Status == store.StatusWorking || m.Status == store.StatusQueued {
		writeErr(w, http.StatusConflict, "this memo is being written out already")
		return
	}
	if in.Language != nil {
		l := strings.ToLower(strings.TrimSpace(*in.Language))
		if l == "auto" {
			l = ""
		}
		if !speech.ValidLanguage(l) {
			writeErr(w, http.StatusBadRequest, "language is a two-letter code like bs or en")
			return
		}
		m.Language = l
	} else if m.Status == store.StatusDone {
		m.Language = ""
	}
	m.Status, m.Error = store.StatusQueued, ""
	if err := s.store.SaveMemo(ctx, uid, m); err != nil {
		s.fail(w, err, "memo")
		return
	}
	s.enqueue()
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}

// handleSkipAction hides (or brings back) one of the AI's suggestions.
func (s *Server) handleSkipAction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	i, err := strconv.Atoi(chi.URLParam(r, "i"))
	if err != nil || i < 0 || i >= len(m.Actions) {
		writeErr(w, http.StatusNotFound, "no such suggestion")
		return
	}
	var in struct {
		Skipped bool `json:"skipped"`
	}
	_ = decode(r, &in)
	m.Actions[i].Skipped = in.Skipped
	if err := s.store.SaveMemo(ctx, uid, m); err != nil {
		s.fail(w, err, "memo")
		return
	}
	writeJSON(w, http.StatusOK, s.memoOut(ctx, m))
}

// ---- for dashboards, the journal and Pocket AI ----

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c := s.store.CountMemos(ctx, uid)
	today := store.Today()
	todays, _, _ := s.store.ListMemos(ctx, uid, store.MemoFilter{From: today, To: today, Limit: 50})
	inbox, _, _ := s.store.ListMemos(ctx, uid, store.MemoFilter{Inbox: true, Limit: 5})
	brief := func(ms []store.Memo) []map[string]any {
		out := []map[string]any{}
		for _, m := range ms {
			out = append(out, map[string]any{"id": m.ID, "title": m.Title, "kind": m.Kind, "recorded_at": m.RecordedAt, "duration_ms": m.DurationMS,
				"status": m.Status, "summary": firstNonEmpty(m.Summary, excerpt(m.Transcript, 160)), "suggestions": openActions(m), "url": s.memoURL(m.ID)})
		}
		return out
	}
	writeJSON(w, http.StatusOK, map[string]any{"memos": c.All, "inbox": c.Inbox, "starred": c.Starred, "working": c.Working, "failed": c.Failed,
		"minutes": c.Minutes, "today": len(todays), "latest_inbox": brief(inbox), "url": strings.TrimRight(s.cfg.BaseURL, "/") + "/"})
}

// openActions is how many suggestions of a memo wait to be sent.
func openActions(m store.Memo) int {
	n := 0
	for _, a := range m.Actions {
		if a.SentTo == "" && !a.Skipped {
			n++
		}
	}
	return n
}

// handleDay is one day's memos — what Pocket Day writes into the journal.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	date := chi.URLParam(r, "date")
	if !validDate(date) || date == "" {
		writeErr(w, http.StatusBadRequest, "the date is YYYY-MM-DD")
		return
	}
	ms, _, err := s.store.ListMemos(ctx, uid, store.MemoFilter{From: date, To: date, Oldest: true, Limit: 200})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	type dayMemo struct {
		ID         int64        `json:"id"`
		Title      string       `json:"title"`
		Kind       string       `json:"kind"`
		RecordedAt string       `json:"recorded_at"`
		Time       string       `json:"time"`
		DurationMS int64        `json:"duration_ms"`
		Summary    string       `json:"summary"`
		Text       string       `json:"text"`
		Status     string       `json:"status"`
		Sent       []store.Sent `json:"sent"`
		URL        string       `json:"url"`
	}
	out := []dayMemo{}
	var total int64
	for _, m := range ms {
		t := ""
		if ts, err := time.Parse(time.RFC3339Nano, m.RecordedAt); err == nil {
			t = ts.Local().Format("15:04")
		}
		total += m.DurationMS
		out = append(out, dayMemo{ID: m.ID, Title: m.Title, Kind: m.Kind, RecordedAt: m.RecordedAt, Time: t, DurationMS: m.DurationMS,
			Summary: m.Summary, Text: excerpt(firstNonEmpty(m.Tidy, m.Transcript), 600), Status: m.Status, Sent: m.Sent, URL: s.memoURL(m.ID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "memos": out, "count": len(out), "duration_ms": total})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 200 {
		writeErr(w, http.StatusBadRequest, "search for 1 to 200 characters")
		return
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	ms, total, err := s.store.ListMemos(ctx, actorID(ctx), store.MemoFilter{Q: q, Limit: limit})
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.memoList(ctx, ms), "total": total})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, err := s.store.Tags(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleKinds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeJSON(w, http.StatusOK, map[string]any{"kinds": store.Kinds, "counts": s.store.KindCounts(ctx, actorID(ctx))})
}

// ---- dictation for other apps: words back, nothing kept ----

// handleTranscribe writes out a short recording and answers with the words.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "send the recording as multipart form field \"file\" (at most 64 MB)")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, fh, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "no recording in field \"file\"")
		return
	}
	defer f.Close()
	ext, _, ok := extFor(fh.Filename, fh.Header.Get("Content-Type"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "that is not a recording Voice can read")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	lang := strings.ToLower(strings.TrimSpace(r.FormValue("language")))
	if lang == "auto" {
		lang = ""
	}
	if lang == "" {
		lang = st.Language
	}
	if !speech.ValidLanguage(lang) {
		writeErr(w, http.StatusBadRequest, "language is a two-letter code like bs or en")
		return
	}
	in := filepath.Join(s.tmpDir(), "dictation-"+randName(ext))
	defer os.Remove(in)
	dst, err := os.Create(in)
	if err != nil {
		s.fail(w, err, "dictation")
		return
	}
	_, err = io.Copy(dst, f)
	_ = dst.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the recording did not arrive whole")
		return
	}
	res, err := s.transcribeFile(ctx, in, st, lang)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if st.Latin && latinLanguage(firstNonEmpty(res.Language, lang)) {
		res.Text = speech.ToLatin(res.Text)
		for i := range res.Segments {
			res.Segments[i].Text = speech.ToLatin(res.Segments[i].Text)
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// transcribeFile writes out one file now with the person's engine.
func (s *Server) transcribeFile(ctx context.Context, path string, st store.Settings, lang string) (*speech.Result, error) {
	prompt := vocabularyPrompt(st.Vocabulary)
	if st.Engine == "api" {
		return speech.NewRemote(st.OpenAIBaseURL, st.OpenAIKey, st.SpeechAPI).Transcribe(ctx, path, lang, prompt)
	}
	if !s.speech.HasWhisper() {
		return nil, errNoWhisper
	}
	if !s.speech.HasFFmpeg() {
		return nil, errors.New("ffmpeg is not installed here")
	}
	model := s.speechModel(ctx)
	if !s.speech.ModelReady(model) {
		if !s.cfg.DemoData {
			s.speech.StartFetch(model)
		}
		return nil, errors.New("the speech model is still being downloaded; try again in a few minutes")
	}
	wav := path + ".wav"
	defer os.Remove(wav)
	if err := s.speech.ToWAV(ctx, path, wav); err != nil {
		return nil, err
	}
	return s.speech.Transcribe(ctx, wav, model, lang, prompt, nil)
}

// handleSpeech says how this person's memos are written out: the engine, the
// box's model and whether it is downloaded, and (for admins) every model.
func (s *Server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	cur := s.speechModel(ctx)
	type modelOut struct {
		speech.Model
		Ready    bool             `json:"ready"`
		Current  bool             `json:"current"`
		Download *speech.Download `json:"download,omitempty"`
	}
	models := []modelOut{}
	for _, m := range speech.Models {
		mo := modelOut{Model: m, Ready: s.speech.ModelReady(m), Current: m.ID == cur.ID}
		if d := s.speech.DownloadState(m.ID); d != nil && !(d.Done && d.Error == "") {
			mo.Download = d
		}
		models = append(models, mo)
	}
	ready := s.speech.HasWhisper() && s.speech.HasFFmpeg() && s.speech.ModelReady(cur)
	why := ""
	switch {
	case st.Engine == "api":
		ready = st.OpenAIKey != "" || (st.OpenAIBaseURL != "" && !strings.Contains(st.OpenAIBaseURL, "api.openai.com"))
		if !ready {
			why = "add your OpenAI API key under Settings → AI, or the address of a speech server"
		}
	case !s.speech.HasWhisper():
		why = errNoWhisper.Error()
	case !s.speech.HasFFmpeg():
		why = "ffmpeg is not installed here"
	case !s.speech.ModelReady(cur):
		why = "the speech model (" + cur.Name + ", about " + strconv.FormatInt(cur.Bytes>>20, 10) + " MB) is downloaded with the first memo"
	}
	writeJSON(w, http.StatusOK, map[string]any{"engine": firstNonEmpty(st.Engine, "local"), "ready": ready, "detail": why,
		"whisper": s.speech.HasWhisper(), "ffmpeg": s.speech.HasFFmpeg(), "model": cur.ID, "models": models, "threads": firstPositive(s.cfg.WhisperThreads, speech.DefaultThreads()),
		"languages": speech.Languages, "api_model": firstNonEmpty(st.SpeechAPI, speech.DefaultRemoteModel), "demo": s.cfg.DemoData})
}

func firstPositive(v ...int) int {
	for _, n := range v {
		if n > 0 {
			return n
		}
	}
	return 0
}

// handleModel downloads (POST) or removes (DELETE) a speech model — admins.
func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	m, ok := speech.ModelByID(chi.URLParam(r, "model"))
	if !ok || chi.URLParam(r, "model") == "" {
		writeErr(w, http.StatusNotFound, "no such model")
		return
	}
	if r.Method == http.MethodDelete {
		if m.ID == s.speechModel(r.Context()).ID {
			writeErr(w, http.StatusConflict, "this is the model in use; pick another one first")
			return
		}
		if err := s.speech.RemoveModel(m); err != nil {
			s.fail(w, err, "remove model")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.cfg.DemoData {
		writeErr(w, http.StatusConflict, "models are not downloaded on a box that shows demo data")
		return
	}
	s.speech.StartFetch(m)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}
