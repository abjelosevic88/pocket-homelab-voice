package store

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
)

// Dictation is one text typed into an app for the person by voice (Pocket
// Launcher's hold-a-key dictation on the Mac): the words and where they went.
type Dictation struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	Ref        string `json:"ref"`
	Text       string `json:"text"`
	Raw        string `json:"raw"`
	App        string `json:"app"`
	AppID      string `json:"app_id"`
	DurationMS int64  `json:"duration_ms"`
	Words      int64  `json:"words"`
	Engine     string `json:"engine"`
	Language   string `json:"language"`
	Device     string `json:"device"`
	Source     string `json:"source"` // the client that recorded it: Pocket Launcher, Pocket AI app, …
	Polished   bool   `json:"polished"`
	DictatedAt string `json:"dictated_at"`
	CreatedAt  string `json:"created_at"`
}

// TypingWPM is the typing speed dictation is measured against for "time saved".
const TypingWPM = 40

// DictationTime is how dictation times are stored: UTC, fixed width (always
// milliseconds), so they sort and compare as text.
func DictationTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// dictDayStart is the stored form of the moment a local day begins.
func dictDayStart(d string) string {
	t, err := time.ParseInLocation("2006-01-02", d, time.Local)
	if err != nil {
		return d
	}
	return DictationTime(t)
}

// CountWords is how many whitespace-separated words a text has.
func CountWords(s string) int64 { return int64(len(strings.Fields(s))) }

// savedSeconds is the time a dictation saved against typing it at TypingWPM.
func savedSeconds(words, durationMS int64) float64 {
	return math.Max(0, float64(words*60)/TypingWPM-float64(durationMS)/1000)
}

const dictationCols = `id, user_id, ref, text, raw, app, app_id, duration_ms, words, engine, language, device, source, polished, dictated_at, created_at`

func scanDictation(r scanner) (*Dictation, error) {
	var d Dictation
	var polished int
	if err := r.Scan(&d.ID, &d.UserID, &d.Ref, &d.Text, &d.Raw, &d.App, &d.AppID, &d.DurationMS, &d.Words, &d.Engine, &d.Language, &d.Device,
		&d.Source, &polished, &d.DictatedAt, &d.CreatedAt); err != nil {
		return nil, err
	}
	d.Polished = polished == 1
	return &d, nil
}

// CreateDictation saves a dictation for uid. A dictation with the same ref
// already there is left as it is and returned instead (created is false).
func (s *Store) CreateDictation(ctx context.Context, uid int64, d *Dictation) (created bool, err error) {
	d.UserID = uid
	d.Words = CountWords(d.Text)
	if d.DictatedAt == "" {
		d.DictatedAt = DictationTime(time.Now())
	}
	now := DictationTime(time.Now())
	res, err := s.DB.ExecContext(ctx, `INSERT INTO dictations (user_id, ref, text, raw, app, app_id, duration_ms, words, engine, language, device, source, polished, search, dictated_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id, ref) DO NOTHING`,
		uid, d.Ref, d.Text, d.Raw, d.App, d.AppID, d.DurationMS, d.Words, d.Engine, d.Language, d.Device, d.Source, boolInt(d.Polished), speech.Fold(d.Text), d.DictatedAt, now)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		have, err := s.DictationByRef(ctx, uid, d.Ref)
		if err != nil {
			return false, err
		}
		*d = *have
		return false, nil
	}
	d.ID, _ = res.LastInsertId()
	d.CreatedAt = now
	return true, nil
}

// DictationByRef finds uid's dictation by the client's ref.
func (s *Store) DictationByRef(ctx context.Context, uid int64, ref string) (*Dictation, error) {
	d, err := scanDictation(s.DB.QueryRowContext(ctx, `SELECT `+dictationCols+` FROM dictations WHERE user_id = ? AND ref = ?`, uid, ref))
	return d, notFound(err)
}

// DeleteDictation removes one of uid's dictations.
func (s *Store) DeleteDictation(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM dictations WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearDictations removes all of uid's dictations and says how many went.
func (s *Store) ClearDictations(ctx context.Context, uid int64) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM dictations WHERE user_id = ?`, uid)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DictationFilter narrows a listing.
type DictationFilter struct {
	Q      string
	App    string
	Source string
	From   string // local day YYYY-MM-DD
	To     string
	Before int64 // id cursor: the page after this dictation
	Limit  int   // 0 = all
	Oldest bool  // exports
}

// ListDictations lists uid's dictations newest first, how many match the
// filter, and the cursor for the next page (0 when there is none).
func (s *Store) ListDictations(ctx context.Context, uid int64, f DictationFilter) ([]Dictation, int, int64, error) {
	where := []string{"user_id = ?"}
	args := []any{uid}
	if q := strings.TrimSpace(f.Q); q != "" {
		for _, w := range strings.Fields(speech.Fold(q)) {
			where = append(where, "search LIKE ?")
			args = append(args, "%"+w+"%")
		}
	}
	if f.App != "" {
		where = append(where, "app = ?")
		args = append(args, f.App)
	}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if f.From != "" {
		where = append(where, "dictated_at >= ?")
		args = append(args, dictDayStart(f.From))
	}
	if f.To != "" {
		where = append(where, "dictated_at < ?")
		args = append(args, dictDayStart(AddDays(f.To, 1)))
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM dictations WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, 0, err
	}
	if f.Before > 0 {
		var at string
		if err := s.DB.QueryRowContext(ctx, `SELECT dictated_at FROM dictations WHERE id = ? AND user_id = ?`, f.Before, uid).Scan(&at); err == nil {
			where = append(where, "(dictated_at < ? OR (dictated_at = ? AND id < ?))")
			args = append(args, at, at, f.Before)
		} else {
			where = append(where, "id < ?") // the cursor row is gone: best effort
			args = append(args, f.Before)
		}
	}
	order := "dictated_at DESC, id DESC"
	if f.Oldest {
		order = "dictated_at, id"
	}
	q := `SELECT ` + dictationCols + ` FROM dictations WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit+1)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	out := []Dictation{}
	for rows.Next() {
		d, err := scanDictation(rows)
		if err != nil {
			return nil, 0, 0, err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	var next int64
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	return out, total, next, nil
}

// DictationPeriod is how much was dictated in a stretch of days.
type DictationPeriod struct {
	Words        int64 `json:"words"`
	Sessions     int64 `json:"sessions"`
	SavedSeconds int64 `json:"saved_seconds"`
}

// DictationApp is an app dictated into and how much.
type DictationApp struct {
	App      string `json:"app"`
	Sessions int64  `json:"sessions"`
	Words    int64  `json:"words"`
}

// DictationDay is one local day's dictation.
type DictationDay struct {
	Date     string `json:"date"`
	Words    int64  `json:"words"`
	Sessions int64  `json:"sessions"`
}

// DictationStats sums up uid's dictation.
type DictationStats struct {
	Words        int64           `json:"words"`
	Sessions     int64           `json:"sessions"`
	DurationMS   int64           `json:"duration_ms"`
	SavedSeconds int64           `json:"saved_seconds"`
	WPM          int64           `json:"wpm"`
	Today        DictationPeriod `json:"today"`
	Week         DictationPeriod `json:"week"`
	Streak       int             `json:"streak"`
	Apps         []DictationApp  `json:"apps"`
	Sources      []DictationApp  `json:"sources"` // the same by where it was dictated (App is the source's name)
	Days         []DictationDay  `json:"days"`
}

// StatsDays is how many days the stats chart covers.
const StatsDays = 30

// DictationStatsFor sums up uid's dictation as of now (days in the server's time zone).
func (s *Store) DictationStatsFor(ctx context.Context, uid int64, now time.Time) (*DictationStats, error) {
	now = now.In(time.Local)
	today := now.Format("2006-01-02")
	weekFrom := AddDays(today, -6)
	chartFrom := AddDays(today, -(StatsDays - 1))
	st := &DictationStats{Apps: []DictationApp{}, Sources: []DictationApp{}, Days: []DictationDay{}}
	var saved, todaySaved, weekSaved float64
	byDay := map[string]*DictationDay{}
	rows, err := s.DB.QueryContext(ctx, `SELECT dictated_at, words, duration_ms FROM dictations WHERE user_id = ?`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var at string
		var words, ms int64
		if err := rows.Scan(&at, &words, &ms); err != nil {
			rows.Close()
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			continue
		}
		day := t.In(time.Local).Format("2006-01-02")
		sv := savedSeconds(words, ms)
		st.Words += words
		st.Sessions++
		st.DurationMS += ms
		saved += sv
		if day == today {
			st.Today.Words += words
			st.Today.Sessions++
			todaySaved += sv
		}
		if day >= weekFrom && day <= today {
			st.Week.Words += words
			st.Week.Sessions++
			weekSaved += sv
		}
		dd := byDay[day]
		if dd == nil {
			dd = &DictationDay{Date: day}
			byDay[day] = dd
		}
		dd.Words += words
		dd.Sessions++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	st.SavedSeconds = int64(math.Round(saved))
	st.Today.SavedSeconds = int64(math.Round(todaySaved))
	st.Week.SavedSeconds = int64(math.Round(weekSaved))
	if st.DurationMS > 0 {
		st.WPM = int64(math.Round(float64(st.Words) / (float64(st.DurationMS) / 60000)))
	}
	// the streak: days in a row with a dictation, up to today (or yesterday,
	// while today has none yet)
	d := today
	if byDay[d] == nil {
		d = AddDays(today, -1)
	}
	for byDay[d] != nil {
		st.Streak++
		d = AddDays(d, -1)
	}
	for d := chartFrom; d <= today; d = AddDays(d, 1) {
		if x := byDay[d]; x != nil {
			st.Days = append(st.Days, *x)
		} else {
			st.Days = append(st.Days, DictationDay{Date: d})
		}
	}
	arows, err := s.DB.QueryContext(ctx, `SELECT app, COUNT(1) AS n, COALESCE(SUM(words),0) FROM dictations WHERE user_id = ? AND app <> '' GROUP BY app ORDER BY n DESC, app LIMIT 8`, uid)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var a DictationApp
		if err := arows.Scan(&a.App, &a.Sessions, &a.Words); err != nil {
			return nil, err
		}
		st.Apps = append(st.Apps, a)
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}
	srows, err := s.DB.QueryContext(ctx, `SELECT source, COUNT(1) AS n, COALESCE(SUM(words),0) FROM dictations WHERE user_id = ? AND source <> '' GROUP BY source ORDER BY n DESC, source LIMIT 8`, uid)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var a DictationApp
		if err := srows.Scan(&a.App, &a.Sessions, &a.Words); err != nil {
			return nil, err
		}
		st.Sources = append(st.Sources, a)
	}
	return st, srows.Err()
}

// DictationWordsOn is how many words uid dictated on a local day.
func (s *Store) DictationWordsOn(ctx context.Context, uid int64, day string) (words, sessions int64) {
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(words),0), COUNT(1) FROM dictations WHERE user_id = ? AND dictated_at >= ? AND dictated_at < ?`,
		uid, dictDayStart(day), dictDayStart(AddDays(day, 1))).Scan(&words, &sessions)
	return
}
