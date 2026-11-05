package store

import (
	"context"
	"encoding/json"
	"strings"
)

// Word is one entry of the person's dictionary: a word to expect (a name, how
// a word is spelled) when ReplaceWith is empty, else a replacement.
type Word struct {
	ID          int64  `json:"id"`
	UserID      int64  `json:"-"`
	Phrase      string `json:"phrase"`
	ReplaceWith string `json:"replace_with"`
	Source      string `json:"source"` // manual | learned
	Uses        int64  `json:"uses"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"-"`
}

// WordKey is how a phrase is compared: lower case, single spaces.
func WordKey(p string) string { return strings.ToLower(strings.Join(strings.Fields(p), " ")) }

const wordCols = `id, user_id, phrase, replace_with, source, uses, created_at, updated_at`

func scanWord(r scanner) (*Word, error) {
	var w Word
	err := r.Scan(&w.ID, &w.UserID, &w.Phrase, &w.ReplaceWith, &w.Source, &w.Uses, &w.CreatedAt, &w.UpdatedAt)
	return &w, err
}

// Words is uid's dictionary, by phrase.
func (s *Store) Words(ctx context.Context, uid int64) ([]Word, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+wordCols+` FROM dictionary WHERE user_id = ? ORDER BY phrase_key, id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Word{}
	for rows.Next() {
		w, err := scanWord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// GetWord reads one of uid's dictionary entries.
func (s *Store) GetWord(ctx context.Context, uid, id int64) (*Word, error) {
	w, err := scanWord(s.DB.QueryRowContext(ctx, `SELECT `+wordCols+` FROM dictionary WHERE id = ? AND user_id = ?`, id, uid))
	return w, notFound(err)
}

// WordByPhrase finds uid's entry for a phrase, whatever its capitals.
func (s *Store) WordByPhrase(ctx context.Context, uid int64, phrase string) (*Word, error) {
	w, err := scanWord(s.DB.QueryRowContext(ctx, `SELECT `+wordCols+` FROM dictionary WHERE user_id = ? AND phrase_key = ?`, uid, WordKey(phrase)))
	return w, notFound(err)
}

// AddWord saves a new entry (ErrConflict when the phrase is there already).
func (s *Store) AddWord(ctx context.Context, uid int64, w *Word) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO dictionary (user_id, phrase, phrase_key, replace_with, source, uses, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		uid, w.Phrase, WordKey(w.Phrase), w.ReplaceWith, w.Source, w.Uses, now, now)
	if err != nil {
		return conflict(err)
	}
	w.ID, _ = res.LastInsertId()
	w.UserID, w.CreatedAt, w.UpdatedAt = uid, now, now
	return nil
}

// SaveWord writes an entry's phrase and replacement.
func (s *Store) SaveWord(ctx context.Context, uid int64, w *Word) error {
	w.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE dictionary SET phrase = ?, phrase_key = ?, replace_with = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		w.Phrase, WordKey(w.Phrase), w.ReplaceWith, w.UpdatedAt, w.ID, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWord removes one of uid's entries.
func (s *Store) DeleteWord(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM dictionary WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountWordUses adds how often each replacement fired (best effort).
func (s *Store) CountWordUses(ctx context.Context, uid int64, uses map[int64]int) {
	for id, n := range uses {
		if n > 0 {
			_, _ = s.DB.ExecContext(ctx, `UPDATE dictionary SET uses = uses + ? WHERE id = ? AND user_id = ?`, n, id, uid)
		}
	}
}

// ---- dictation styles ----

// Dictation styles: how polishing treats the words for an app.
const (
	StyleNatural = "natural" // what polishing always did
	StyleCasual  = "casual"  // relaxed, for chat
	StyleFormal  = "formal"  // complete sentences, for email and documents
	StyleCode    = "code"    // identifiers, paths and commands kept exactly
	StyleNone    = "none"    // no AI at all
)

// Styles lists the dictation styles.
var Styles = []string{StyleNatural, StyleCasual, StyleFormal, StyleCode, StyleNone}

// ValidStyle accepts one of Styles.
func ValidStyle(v string) bool {
	for _, x := range Styles {
		if x == v {
			return true
		}
	}
	return false
}

// AppStyle is the style for one app (by its bundle id, or its name).
type AppStyle struct {
	AppID string `json:"app_id"`
	App   string `json:"app"`
	Style string `json:"style"`
}

// DictationStyles are a person's styles.
type DictationStyles struct {
	Default string     `json:"default"`
	Apps    []AppStyle `json:"apps"`
}

// GetDictationStyles reads uid's styles (natural for everything when unset).
func (s *Store) GetDictationStyles(ctx context.Context, uid int64) DictationStyles {
	out := DictationStyles{Default: StyleNatural, Apps: []AppStyle{}}
	var def, apps string
	if err := s.DB.QueryRowContext(ctx, `SELECT default_style, apps FROM dictation_styles WHERE user_id = ?`, uid).Scan(&def, &apps); err != nil {
		return out
	}
	if ValidStyle(def) {
		out.Default = def
	}
	_ = json.Unmarshal([]byte(apps), &out.Apps)
	if out.Apps == nil {
		out.Apps = []AppStyle{}
	}
	return out
}

// SaveDictationStyles replaces uid's styles.
func (s *Store) SaveDictationStyles(ctx context.Context, uid int64, st DictationStyles) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO dictation_styles (user_id, default_style, apps) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET default_style = excluded.default_style, apps = excluded.apps`, uid, st.Default, toJSON(st.Apps))
	return err
}

// Match is the style set for an app, given by bundle id or by name.
func (st DictationStyles) Match(app string) (string, bool) {
	app = strings.TrimSpace(app)
	if app != "" {
		for _, a := range st.Apps {
			if (a.AppID != "" && strings.EqualFold(a.AppID, app)) || (a.App != "" && strings.EqualFold(a.App, app)) {
				return a.Style, true
			}
		}
	}
	return "", false
}

// StyleFor is the style for the first of these (bundle id, name) that has one, else the default.
func (st DictationStyles) StyleFor(apps ...string) string {
	for _, a := range apps {
		if v, ok := st.Match(a); ok {
			return v
		}
	}
	return st.Default
}
