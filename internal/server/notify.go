package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// pushes runs every minute; once a day, at each person's own hour, it says
// how many memos wait to be filed (when they want that). "Written out" and
// "could not be written out" go out on their own as it happens (worker.go).
func (s *Server) pushes(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil || !st.NotifyInbox || now.Hour() != st.NotifyHour || now.Minute() > 10 {
			continue
		}
		if !s.once(ctx, fmt.Sprintf("push:%d:%s", uid, store.Today())) {
			continue
		}
		s.dailyCheck(ctx, uid, st, now)
	}
}

// once is true the first time it is asked for a key.
func (s *Server) once(ctx context.Context, key string) bool {
	if _, _, done := s.store.CacheGet(ctx, key); done {
		return false
	}
	_ = s.store.CachePut(ctx, key, "1")
	return true
}

// dailyCheck builds and sends one person's morning message: the memos older
// than a day that still wait in the inbox.
func (s *Server) dailyCheck(ctx context.Context, uid int64, st store.Settings, now time.Time) (string, bool) {
	ms, total, _ := s.store.ListMemos(ctx, uid, store.MemoFilter{Inbox: true, To: store.AddDays(store.Today(), -1), Limit: 5})
	if total == 0 {
		return "", false
	}
	lines := []string{fmt.Sprintf("%d %s %s to be filed:", total, plural(total, "memo", "memos"), plural(total, "waits", "wait"))}
	for _, m := range ms {
		line := "• " + firstNonEmpty(m.Title, "Memo")
		if n := openActions(m); n > 0 {
			line += fmt.Sprintf(" (%d %s)", n, plural(n, "suggestion", "suggestions"))
		}
		lines = append(lines, line)
	}
	if total > len(ms) {
		lines = append(lines, fmt.Sprintf("… and %d more", total-len(ms)))
	}
	body := strings.Join(lines, "\n")
	if st.NotifyURL == "" {
		return body, false
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Voice memos to file", Body: body, Priority: "info", Tags: []string{"studio_microphone"}, Link: strings.TrimRight(s.cfg.BaseURL, "/") + "/inbox"}); err != nil {
		s.log.Warn("notify", "user", uid, "err", err)
		return body, false
	}
	return body, true
}

// pushMemo tells the person about one memo.
func (s *Server) pushMemo(ctx context.Context, uid int64, st store.Settings, m *store.Memo, title, body string) {
	if st.NotifyURL == "" {
		return
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: title, Body: body, Priority: "info", Tags: []string{"studio_microphone"}, Link: s.memoURL(m.ID)}); err != nil {
		s.log.Warn("notify", "user", uid, "err", err)
	}
}

// publishMemo puts a memo event on the Core bus (Pocket Flows, Pocket Day…).
func (s *Server) publishMemo(ctx context.Context, m *store.Memo, topic string) {
	p := map[string]any{"memo_id": m.ID, "title": m.Title, "kind": m.Kind, "recorded_at": m.RecordedAt, "duration_ms": m.DurationMS,
		"language": m.Language, "source": m.Source, "url": s.memoURL(m.ID)}
	if topic == "voice.memo_transcribed" {
		p["text"] = excerpt(m.Transcript, 2000)
		p["engine"] = m.Engine
	}
	s.publish(ctx, m.UserID, topic, fmt.Sprintf("memo:%d", m.ID), p)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Voice", Body: notifyTestBody, Priority: "info", Tags: []string{"studio_microphone"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

const notifyTestBody = "Notifications work. A long memo written out, a memo that could not be, and the morning list of memos to file will arrive here."
