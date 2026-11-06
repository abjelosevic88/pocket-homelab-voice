package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
)

const memoCols = `id, user_id, title, title_set, recorded_at, duration_ms, audio, audio_type, audio_bytes, peaks, language, status, error,
	transcript, segments, edited, engine, summary, tidy, kind, kind_set, actions, ai_status, ai_error, ai_provider, tags, starred, filed,
	source, source_ref, audio_gone_at, meeting, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanMemo(r scanner) (*Memo, error) {
	var m Memo
	var titleSet, edited, kindSet, starred, filed, meeting int
	var peaks, segs, actions string
	if err := r.Scan(&m.ID, &m.UserID, &m.Title, &titleSet, &m.RecordedAt, &m.DurationMS, &m.Audio, &m.AudioType, &m.AudioBytes, &peaks, &m.Language,
		&m.Status, &m.Error, &m.Transcript, &segs, &edited, &m.Engine, &m.Summary, &m.Tidy, &m.Kind, &kindSet, &actions, &m.AIStatus, &m.AIError,
		&m.AIProvider, &m.Tags, &starred, &filed, &m.Source, &m.SourceRef, &m.AudioGoneAt, &meeting, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return nil, err
	}
	m.TitleSet, m.Edited, m.KindSet, m.Starred, m.Filed = titleSet == 1, edited == 1, kindSet == 1, starred == 1, filed == 1
	m.Meeting = meeting == 1
	m.HasAudio = m.Audio != ""
	_ = json.Unmarshal([]byte(peaks), &m.Peaks)
	_ = json.Unmarshal([]byte(segs), &m.Segments)
	_ = json.Unmarshal([]byte(actions), &m.Actions)
	if m.Peaks == nil {
		m.Peaks = []int{}
	}
	if m.Segments == nil {
		m.Segments = []Segment{}
	}
	if m.Actions == nil {
		m.Actions = []Action{}
	}
	m.Sent = []Sent{}
	return &m, nil
}

func searchText(m *Memo) string {
	return speech.Fold(strings.Join([]string{m.Title, m.Transcript, m.Tidy, m.Summary, m.Tags}, " \n"))
}

func (m *Memo) clean() {
	m.Title = strings.TrimSpace(m.Title)
	m.Tags = JoinTags(m.Tags)
	if m.Peaks == nil {
		m.Peaks = []int{}
	}
	if m.Segments == nil {
		m.Segments = []Segment{}
	}
	if m.Actions == nil {
		m.Actions = []Action{}
	}
	if m.RecordedAt == "" {
		m.RecordedAt = Now()
	}
	if m.Status == "" {
		m.Status = StatusQueued
	}
}

// CreateMemo saves a new memo for uid.
func (s *Store) CreateMemo(ctx context.Context, uid int64, m *Memo) error {
	m.clean()
	m.UserID = uid
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO memos (user_id, title, title_set, recorded_at, duration_ms, audio, audio_type, audio_bytes, peaks, language, status, error,
		transcript, segments, edited, engine, summary, tidy, kind, kind_set, actions, ai_status, ai_error, ai_provider, tags, starred, filed, search,
		source, source_ref, audio_gone_at, meeting, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		uid, m.Title, boolInt(m.TitleSet), m.RecordedAt, m.DurationMS, m.Audio, m.AudioType, m.AudioBytes, toJSON(m.Peaks), m.Language, m.Status, m.Error,
		m.Transcript, toJSON(m.Segments), boolInt(m.Edited), m.Engine, m.Summary, m.Tidy, m.Kind, boolInt(m.KindSet), toJSON(m.Actions), m.AIStatus, m.AIError,
		m.AIProvider, m.Tags, boolInt(m.Starred), boolInt(m.Filed), searchText(m), m.Source, m.SourceRef, m.AudioGoneAt, boolInt(m.Meeting), now, now)
	if err != nil {
		return conflict(err)
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt, m.UpdatedAt = now, now
	m.HasAudio = m.Audio != ""
	if m.Sent == nil {
		m.Sent = []Sent{}
	}
	return nil
}

// SaveMemo writes every field of a memo that belongs to uid.
func (s *Store) SaveMemo(ctx context.Context, uid int64, m *Memo) error {
	m.clean()
	m.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE memos SET title=?, title_set=?, recorded_at=?, duration_ms=?, audio=?, audio_type=?, audio_bytes=?, peaks=?, language=?,
		status=?, error=?, transcript=?, segments=?, edited=?, engine=?, summary=?, tidy=?, kind=?, kind_set=?, actions=?, ai_status=?, ai_error=?, ai_provider=?,
		tags=?, starred=?, filed=?, search=?, audio_gone_at=?, meeting=?, updated_at=? WHERE id=? AND user_id=?`,
		m.Title, boolInt(m.TitleSet), m.RecordedAt, m.DurationMS, m.Audio, m.AudioType, m.AudioBytes, toJSON(m.Peaks), m.Language,
		m.Status, m.Error, m.Transcript, toJSON(m.Segments), boolInt(m.Edited), m.Engine, m.Summary, m.Tidy, m.Kind, boolInt(m.KindSet), toJSON(m.Actions),
		m.AIStatus, m.AIError, m.AIProvider, m.Tags, boolInt(m.Starred), boolInt(m.Filed), searchText(m), m.AudioGoneAt, boolInt(m.Meeting), m.UpdatedAt, m.ID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	m.HasAudio = m.Audio != ""
	return nil
}

// GetMemo reads one of uid's memos with where it went.
func (s *Store) GetMemo(ctx context.Context, uid, id int64) (*Memo, error) {
	m, err := scanMemo(s.DB.QueryRowContext(ctx, `SELECT `+memoCols+` FROM memos WHERE id = ? AND user_id = ?`, id, uid))
	if err != nil {
		return nil, notFound(err)
	}
	m.Sent, err = s.ListSent(ctx, uid, id)
	return m, err
}

// MemoBySource finds the memo another app (or an import) made under its own reference.
func (s *Store) MemoBySource(ctx context.Context, uid int64, source, ref string) (*Memo, error) {
	m, err := scanMemo(s.DB.QueryRowContext(ctx, `SELECT `+memoCols+` FROM memos WHERE user_id = ? AND source = ? AND source_ref = ?`, uid, source, ref))
	return m, notFound(err)
}

// DeleteMemo removes one of uid's memos and returns its audio file name.
func (s *Store) DeleteMemo(ctx context.Context, uid, id int64) (string, error) {
	var audio string
	if err := s.DB.QueryRowContext(ctx, `SELECT audio FROM memos WHERE id = ? AND user_id = ?`, id, uid).Scan(&audio); err != nil {
		return "", notFound(err)
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM memos WHERE id = ? AND user_id = ?`, id, uid)
	return audio, err
}

// MemoFilter narrows a listing.
type MemoFilter struct {
	Q       string
	Kind    string
	Tag     string
	Inbox   bool // not filed yet
	Starred bool
	Status  string
	From    string // local day YYYY-MM-DD
	To      string
	Source  string
	Limit   int
	Offset  int
	Oldest  bool
	Full    bool // with segments (exports)
}

// dayStart is the UTC moment a local day begins.
func dayStart(d string) string {
	t, err := time.ParseInLocation("2006-01-02", d, time.Local)
	if err != nil {
		return d
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// ListMemos lists uid's memos (newest first) and how many match.
func (s *Store) ListMemos(ctx context.Context, uid int64, f MemoFilter) ([]Memo, int, error) {
	where := []string{"user_id = ?"}
	args := []any{uid}
	if q := strings.TrimSpace(f.Q); q != "" {
		for _, w := range strings.Fields(speech.Fold(q)) {
			where = append(where, "search LIKE ?")
			args = append(args, "%"+w+"%")
		}
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Tag != "" {
		where = append(where, "(', ' || tags || ',') LIKE ?")
		args = append(args, "%, "+strings.ToLower(f.Tag)+",%")
	}
	if f.Inbox {
		where = append(where, "filed = 0")
	}
	if f.Starred {
		where = append(where, "starred = 1")
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if f.From != "" {
		where = append(where, "recorded_at >= ?")
		args = append(args, dayStart(f.From))
	}
	if f.To != "" {
		where = append(where, "recorded_at < ?")
		args = append(args, dayStart(AddDays(f.To, 1)))
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM memos WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "recorded_at DESC, id DESC"
	if f.Oldest {
		order = "recorded_at, id"
	}
	q := `SELECT ` + memoCols + ` FROM memos WHERE ` + w + ` ORDER BY ` + order
	if f.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	out := []Memo{}
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		if !f.Full {
			m.Segments = []Segment{}
		}
		out = append(out, *m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) > 0 {
		sent, err := s.sentFor(ctx, uid, out)
		if err != nil {
			return nil, 0, err
		}
		for i := range out {
			if v, ok := sent[out[i].ID]; ok {
				out[i].Sent = v
			}
		}
	}
	return out, total, nil
}

func (s *Store) sentFor(ctx context.Context, uid int64, ms []Memo) (map[int64][]Sent, error) {
	ids := make([]string, len(ms))
	args := []any{uid}
	for i, m := range ms {
		ids[i] = "?"
		args = append(args, m.ID)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, memo_id, app, kind, ref, title, url, created_at FROM sent WHERE user_id = ? AND memo_id IN (`+strings.Join(ids, ",")+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]Sent{}
	for rows.Next() {
		var x Sent
		if err := rows.Scan(&x.ID, &x.UserID, &x.MemoID, &x.App, &x.Kind, &x.Ref, &x.Title, &x.URL, &x.CreatedAt); err != nil {
			return nil, err
		}
		out[x.MemoID] = append(out[x.MemoID], x)
	}
	return out, rows.Err()
}

// ListSent is where one memo went.
func (s *Store) ListSent(ctx context.Context, uid, memoID int64) ([]Sent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, memo_id, app, kind, ref, title, url, created_at FROM sent WHERE user_id = ? AND memo_id = ? ORDER BY id`, uid, memoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sent{}
	for rows.Next() {
		var x Sent
		if err := rows.Scan(&x.ID, &x.UserID, &x.MemoID, &x.App, &x.Kind, &x.Ref, &x.Title, &x.URL, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// AllSent is everything uid sent anywhere (exports).
func (s *Store) AllSent(ctx context.Context, uid int64) ([]Sent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, memo_id, app, kind, ref, title, url, created_at FROM sent WHERE user_id = ? ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sent{}
	for rows.Next() {
		var x Sent
		if err := rows.Scan(&x.ID, &x.UserID, &x.MemoID, &x.App, &x.Kind, &x.Ref, &x.Title, &x.URL, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// AddSent records that (part of) a memo went to another app.
func (s *Store) AddSent(ctx context.Context, uid int64, x *Sent) error {
	x.UserID = uid
	if x.CreatedAt == "" {
		x.CreatedAt = Now()
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sent (user_id, memo_id, app, kind, ref, title, url, created_at) SELECT ?, id, ?, ?, ?, ?, ?, ? FROM memos WHERE id = ? AND user_id = ?`,
		uid, x.App, x.Kind, x.Ref, x.Title, x.URL, x.CreatedAt, x.MemoID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	x.ID, _ = res.LastInsertId()
	return nil
}

// ---- the queue (box-wide: one recording at a time, oldest first) ----

// NextQueued is the memo waiting longest to be written out, marked working.
func (s *Store) NextQueued(ctx context.Context) (*Memo, error) {
	m, err := scanMemo(s.DB.QueryRowContext(ctx, `SELECT `+memoCols+` FROM memos WHERE status = 'queued' ORDER BY created_at, id LIMIT 1`))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE memos SET status = 'working', error = '', updated_at = ? WHERE id = ?`, Now(), m.ID); err != nil {
		return nil, err
	}
	m.Status = StatusWorking
	return m, nil
}

// ResetWorking puts memos that were being written out when the app stopped back in the queue.
func (s *Store) ResetWorking(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE memos SET status = 'queued' WHERE status = 'working'`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE memos SET ai_status = '' WHERE ai_status = 'working'`)
	return err
}

// QueuePosition is how many memos (anyone's) are ahead of this one.
func (s *Store) QueuePosition(ctx context.Context, id int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM memos WHERE status IN ('queued','working') AND (created_at, id) < (SELECT created_at, id FROM memos WHERE id = ?)`, id).Scan(&n)
	return n
}

// Counts per person for the sidebar and summaries.
type MemoCounts struct {
	All     int   `json:"all"`
	Inbox   int   `json:"inbox"`
	Starred int   `json:"starred"`
	Working int   `json:"working"`
	Failed  int   `json:"failed"`
	Minutes int64 `json:"minutes"`
}

// CountMemos counts uid's memos.
func (s *Store) CountMemos(ctx context.Context, uid int64) MemoCounts {
	var c MemoCounts
	var ms sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(filed = 0),0), COALESCE(SUM(starred),0), COALESCE(SUM(status IN ('queued','working')),0), COALESCE(SUM(status = 'failed'),0), SUM(duration_ms) FROM memos WHERE user_id = ?`, uid).
		Scan(&c.All, &c.Inbox, &c.Starred, &c.Working, &c.Failed, &ms)
	c.Minutes = (ms.Int64 + 30000) / 60000
	return c
}

// KindCounts is how many memos uid has of each kind.
func (s *Store) KindCounts(ctx context.Context, uid int64) map[string]int {
	out := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind, COUNT(1) FROM memos WHERE user_id = ? AND kind <> '' GROUP BY kind`, uid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			out[k] = n
		}
	}
	return out
}

// TagCount is a tag and how many memos have it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// Tags lists uid's tags, most used first.
func (s *Store) Tags(ctx context.Context, uid int64) ([]TagCount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tags FROM memos WHERE user_id = ? AND tags <> ''`, uid)
	if err != nil {
		return nil, err
	}
	n := map[string]int{}
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			for _, x := range SplitTags(t) {
				n[x]++
			}
		}
	}
	rows.Close()
	out := []TagCount{}
	for t, c := range n {
		out = append(out, TagCount{t, c})
	}
	sortTags(out)
	return out, rows.Err()
}

func sortTags(t []TagCount) {
	for i := 1; i < len(t); i++ {
		for j := i; j > 0 && (t[j].Count > t[j-1].Count || (t[j].Count == t[j-1].Count && t[j].Tag < t[j-1].Tag)); j-- {
			t[j], t[j-1] = t[j-1], t[j]
		}
	}
}

// AudioOlderThan lists uid's memos with a recording made before the moment
// (Settings → keep recordings), done ones only.
func (s *Store) AudioOlderThan(ctx context.Context, uid int64, before time.Time) ([]Memo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+memoCols+` FROM memos WHERE user_id = ? AND audio <> '' AND status = 'done' AND recorded_at < ?`, uid, before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memo{}
	for rows.Next() {
		m, err := scanMemo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// AudioFiles is every recording file uid has (to clean up after them).
func (s *Store) AudioFiles(ctx context.Context, uid int64) map[string]bool {
	out := map[string]bool{}
	rows, err := s.DB.QueryContext(ctx, `SELECT audio FROM memos WHERE user_id = ? AND audio <> ''`, uid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if rows.Scan(&a) == nil {
			out[a] = true
		}
	}
	return out
}

// MemoRef points at one person's memo.
type MemoRef struct{ ID, UserID int64 }

// MeetingMemos are every meeting written out and not corrected by hand.
func (s *Store) MeetingMemos(ctx context.Context) ([]MemoRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id FROM memos WHERE meeting=1 AND edited=0 AND status='done' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoRef
	for rows.Next() {
		var r MemoRef
		if err := rows.Scan(&r.ID, &r.UserID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
