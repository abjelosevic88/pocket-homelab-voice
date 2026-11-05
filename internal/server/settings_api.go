package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat, "week_start": st.WeekStart, "currency": st.Currency,
		"speech_model": s.speechModel(ctx).ID, "dictation_model": s.dictationModel(ctx).ID, "engine": firstNonEmpty(st.Engine, "local"), "language": st.Language, "vocabulary": st.Vocabulary, "latin": st.Latin,
		"speech_api_model": st.SpeechAPI, "keep_audio_days": st.KeepAudio, "playback_speed": firstNonEmpty(st.PlaybackSpd, "1"),
		"ai_tidy": st.AITidy, "file_on_send": st.FileOnSend,
		"notes_folder": st.NotesFolder, "notes_listen": st.NotesListen, "tasks_list_id": st.TasksListID, "calendar_id": st.CalendarID,
		"money_account_id": st.MoneyAccountID, "money_category_id": st.MoneyCategoryID,
		"notify_done": st.NotifyDone, "notify_failed": st.NotifyFailed, "notify_inbox": st.NotifyInbox, "notify_hour": st.NotifyHour,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL, "demo": s.cfg.DemoData,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, url, token, public := connSettings(st, a)
		out[a.slug+"_url"], out[a.slug+"_token"], out[a.slug+"_public_url"] = url, mask(token), public
		if public == "" && via != "" {
			public = s.resolvePublic(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	raw := map[string]any{}
	var in store.Settings
	keys, err := decodeKeys(r, &raw)
	if err != nil || remarshal(raw, &in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	if keys["week_start"] && (in.WeekStart == 1 || in.WeekStart == 7 || in.WeekStart == 6) {
		cur.WeekStart = in.WeekStart
	}
	if keys["currency"] {
		if c := cleanCurrency(in.Currency); c != "" {
			cur.Currency = c
		} else {
			bad("currency is a three-letter code like EUR")
			return
		}
	}
	if keys["keep_audio_days"] {
		if in.KeepAudio < 0 || in.KeepAudio > 3650 {
			bad("keep recordings 0 (forever) to 3650 days")
			return
		}
		cur.KeepAudio = in.KeepAudio
	}
	if keys["notify_hour"] {
		if in.NotifyHour < 0 || in.NotifyHour > 23 {
			bad("the hour is 0 to 23")
			return
		}
		cur.NotifyHour = in.NotifyHour
	}
	if keys["engine"] {
		if in.Engine != "local" && in.Engine != "api" {
			bad("writing out is on this server (local) or through an API (api)")
			return
		}
		cur.Engine = in.Engine
	}
	if keys["language"] {
		l := strings.ToLower(strings.TrimSpace(in.Language))
		if l == "auto" {
			l = ""
		}
		if !speech.ValidLanguage(l) {
			bad("the language is a two-letter code like bs or en")
			return
		}
		cur.Language = l
	}
	if keys["playback_speed"] && oneOf(in.PlaybackSpd, []string{"1", "1.25", "1.5", "1.75", "2"}) {
		cur.PlaybackSpd = in.PlaybackSpd
	}
	if keys["dictation_model"] && isAdmin {
		m, ok := speech.ModelByID(in.DictationModel)
		if !ok || in.DictationModel == "" {
			bad("unknown speech model")
			return
		}
		cur.DictationModel = in.DictationModel
		if !s.cfg.DemoData && !s.speech.ModelReady(m) {
			s.speech.StartFetch(m)
		}
	}
	if keys["speech_model"] && isAdmin {
		if _, ok := speech.ModelByID(in.SpeechModel); !ok || in.SpeechModel == "" {
			bad("unknown speech model")
			return
		}
		cur.SpeechModel = in.SpeechModel
		if !s.cfg.DemoData {
			if m, _ := speech.ModelByID(in.SpeechModel); !s.speech.ModelReady(m) {
				s.speech.StartFetch(m)
			}
		}
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	idIn := func(k string, dst *int64, v int64) {
		if keys[k] && v >= 0 {
			*dst = v
		}
	}
	text("vocabulary", &cur.Vocabulary, in.Vocabulary)
	boolIn("latin", &cur.Latin, in.Latin)
	text("speech_api_model", &cur.SpeechAPI, in.SpeechAPI)
	boolIn("ai_tidy", &cur.AITidy, in.AITidy)
	boolIn("file_on_send", &cur.FileOnSend, in.FileOnSend)
	if keys["notes_folder"] {
		cur.NotesFolder = strings.Trim(strings.TrimSpace(in.NotesFolder), "/")
	}
	boolIn("notes_listen", &cur.NotesListen, in.NotesListen)
	idIn("tasks_list_id", &cur.TasksListID, in.TasksListID)
	idIn("calendar_id", &cur.CalendarID, in.CalendarID)
	idIn("money_account_id", &cur.MoneyAccountID, in.MoneyAccountID)
	idIn("money_category_id", &cur.MoneyCategoryID, in.MoneyCategoryID)
	boolIn("notify_done", &cur.NotifyDone, in.NotifyDone)
	boolIn("notify_failed", &cur.NotifyFailed, in.NotifyFailed)
	boolIn("notify_inbox", &cur.NotifyInbox, in.NotifyInbox)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	// each Pocket app: mode, URL, token, public URL
	inMap := map[string]string{}
	for k, v := range raw {
		if sv, ok := v.(string); ok {
			inMap[k] = sv
		}
	}
	for _, a := range pocketApps {
		c := cur.Conns[a.slug]
		if v := inMap[a.slug+"_mode"]; keys[a.slug+"_mode"] && (v == modeCore || v == modeDirect || v == modeOff) {
			c.Mode = v
		}
		url(a.slug+"_url", &c.URL, inMap[a.slug+"_url"])
		secret(a.slug+"_token", &c.Token, inMap[a.slug+"_token"])
		url(a.slug+"_public_url", &c.PublicURL, inMap[a.slug+"_public_url"])
		if c != (store.Conn{}) {
			cur.Conns[a.slug] = c
		}
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && (keys["title"] || keys["speech_model"] || keys["dictation_model"]) {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for k := range keys {
		if isModeKey(k) {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				msg = fmt.Sprintf("%s %s, %s", a.name, v, how)
				switch a.slug {
				case "money":
					var accts []pocket.Account
					if accts, err = conn.client.Accounts(ctx); err == nil {
						msg += fmt.Sprintf(", %d accounts you can book on", len(accts))
					}
				case "calendar":
					var cals []pocket.Calendar
					if cals, err = conn.client.Calendars(ctx); err == nil {
						msg += fmt.Sprintf(", %d calendars you can write to", len(cals))
					}
				case "notes":
					var fs []pocket.Folder
					if fs, err = conn.client.Folders(ctx); err == nil {
						msg += fmt.Sprintf(", %d folders", len(fs))
					}
				case "tasks":
					var ls []pocket.TaskList
					if ls, err = conn.client.TaskLists(ctx); err == nil {
						msg += fmt.Sprintf(", %d lists", len(ls))
					}
				}
			}
		}
	} else if in.Which == "openai" {
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Voice", Body: notifyTestBody, Priority: "info", Tags: []string{"studio_microphone"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// handleCheckNow runs the daily check for the caller now and says what it sent.
func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	body, sent := s.dailyCheck(ctx, uid, st, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "body": body})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-voice-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's data (the words, not the
// recordings: those come with the ZIP export).
type exportDoc struct {
	App        string            `json:"app"`
	Version    string            `json:"version"`
	Exported   string            `json:"exported_at"`
	Memos      []store.Memo      `json:"memos"`
	Sent       []store.Sent      `json:"sent"`
	Dictations []store.Dictation `json:"dictations"`
	Dictionary []store.Word      `json:"dictionary"`
	// how dictation is polished, per app
	DictationStyles *store.DictationStyles `json:"dictation_styles,omitempty"`
}

func (s *Server) export(ctx context.Context, uid int64) exportDoc {
	doc := exportDoc{App: "pocket-voice", Version: Version, Exported: store.Now()}
	doc.Memos, _, _ = s.store.ListMemos(ctx, uid, store.MemoFilter{Oldest: true, Full: true})
	doc.Sent, _ = s.store.AllSent(ctx, uid)
	doc.Dictations, _, _, _ = s.store.ListDictations(ctx, uid, store.DictationFilter{Oldest: true})
	doc.Dictionary, _ = s.store.Words(ctx, uid)
	styles := s.store.GetDictationStyles(ctx, uid)
	doc.DictationStyles = &styles
	for i := range doc.Memos {
		doc.Memos[i].Sent = []store.Sent{}
	}
	return doc
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-voice-export.json"`)
	writeJSON(w, http.StatusOK, s.export(r.Context(), actorID(r.Context())))
}

// handleExportZip streams every recording with its words: one folder per
// memo-day, "<time> <title>.m4a" plus ".txt", and export.json.
func (s *Server) handleExportZip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := s.export(ctx, uid)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-voice-recordings.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	if f, err := zw.Create("export.json"); err == nil {
		_ = json.NewEncoder(f).Encode(doc)
	}
	used := map[string]int{}
	for _, m := range doc.Memos {
		t, _ := time.Parse(time.RFC3339Nano, m.RecordedAt)
		t = t.Local()
		base := t.Format("2006-01-02") + "/" + t.Format("15.04") + " " + firstNonEmpty(safeFileName(m.Title), "Memo")
		if used[base]++; used[base] > 1 {
			base += fmt.Sprintf(" (%d)", used[base])
		}
		if txt := firstNonEmpty(m.Tidy, m.Transcript); txt != "" {
			if f, err := zw.Create(base + ".txt"); err == nil {
				_, _ = f.Write([]byte(txt + "\n"))
			}
		}
		if m.Audio == "" {
			continue
		}
		src, err := os.Open(s.audioPath(uid, m.Audio))
		if err != nil {
			continue
		}
		hdr := &zip.FileHeader{Name: base + filepath.Ext(m.Audio), Method: zip.Store, Modified: t}
		if f, err := zw.CreateHeader(hdr); err == nil {
			_, _ = io.Copy(f, src)
		}
		src.Close()
	}
}

// handleImport merges a Pocket Voice export (the words; recordings come back
// as files through "Add recordings") into the caller's own memos. Memos keep
// their identity, so importing the same file twice adds nothing.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	b, err := s.readBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the file is too big")
		return
	}
	var doc exportDoc
	if err := json.Unmarshal(b, &doc); err != nil || doc.App != "pocket-voice" {
		writeErr(w, http.StatusBadRequest, "that is not a Pocket Voice export")
		return
	}
	writeJSON(w, http.StatusOK, s.importPocket(ctx, uid, doc))
}

func (s *Server) importPocket(ctx context.Context, uid int64, doc exportDoc) map[string]int {
	counts := map[string]int{"memos": 0, "sent": 0, "dictations": 0, "dictionary": 0}
	ids := map[int64]int64{}
	for _, m := range doc.Memos {
		old := m.ID
		ref := fmt.Sprintf("pocket:%d", old)
		if prev, err := s.store.MemoBySource(ctx, uid, "import", ref); err == nil {
			ids[old] = prev.ID
			continue
		}
		m.Source, m.SourceRef = "import", ref
		m.Audio, m.AudioType, m.AudioBytes = "", "", 0
		if m.Status != store.StatusDone {
			m.Status = store.StatusDone
		}
		for i := range m.Actions {
			if m.Actions[i].SentTo != "" {
				m.Actions[i].Skipped = true
			}
		}
		if err := s.store.CreateMemo(ctx, uid, &m); err == nil {
			ids[old] = m.ID
			counts["memos"]++
		}
	}
	for _, x := range doc.Sent {
		id, ok := ids[x.MemoID]
		if !ok {
			continue
		}
		if have, _ := s.store.ListSent(ctx, uid, id); len(have) > 0 && sentHas(have, x) {
			continue
		}
		x.MemoID = id
		if err := s.store.AddSent(ctx, uid, &x); err == nil {
			counts["sent"]++
		}
	}
	// dictations keep their ref: importing twice (or alongside the live
	// history) adds each one once
	for _, d := range doc.Dictations {
		d.Ref, d.Text = strings.TrimSpace(d.Ref), strings.TrimSpace(d.Text)
		if d.Ref == "" || d.Text == "" {
			continue
		}
		d.ID = 0
		if t, err := time.Parse(time.RFC3339Nano, d.DictatedAt); err == nil {
			d.DictatedAt = store.DictationTime(t)
		} else {
			d.DictatedAt = ""
		}
		if created, err := s.store.CreateDictation(ctx, uid, &d); err == nil && created {
			counts["dictations"]++
		}
	}
	for _, wd := range doc.Dictionary {
		phrase, with, why := cleanWord(wd.Phrase, wd.ReplaceWith)
		if why != "" {
			continue
		}
		if _, err := s.store.WordByPhrase(ctx, uid, phrase); err == nil {
			continue
		}
		src := wd.Source
		if src != "learned" {
			src = "manual"
		}
		if err := s.store.AddWord(ctx, uid, &store.Word{Phrase: phrase, ReplaceWith: with, Source: src, Uses: wd.Uses}); err == nil {
			counts["dictionary"]++
		}
	}
	// styles come along only when none were set here
	if st := doc.DictationStyles; st != nil && store.ValidStyle(st.Default) {
		if cur := s.store.GetDictationStyles(ctx, uid); cur.Default == store.StyleNatural && len(cur.Apps) == 0 {
			ok := []store.AppStyle{}
			for _, a := range st.Apps {
				if store.ValidStyle(a.Style) && (a.AppID != "" || a.App != "") {
					ok = append(ok, a)
				}
			}
			_ = s.store.SaveDictationStyles(ctx, uid, store.DictationStyles{Default: st.Default, Apps: ok})
		}
	}
	return counts
}

func sentHas(list []store.Sent, x store.Sent) bool {
	for _, y := range list {
		if y.App == x.App && y.Kind == x.Kind && y.Ref == x.Ref {
			return true
		}
	}
	return false
}
