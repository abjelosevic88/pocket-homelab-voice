package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// MaxVoices is how many voice prints a person keeps (the newest).
const MaxVoices = 30

const personCols = `id, user_id, name, voices, created_at, updated_at`

func scanPerson(r scanner) (*Person, error) {
	var p Person
	var voices string
	if err := r.Scan(&p.ID, &p.UserID, &p.Name, &voices, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(voices), &p.Voices)
	if p.Voices == nil {
		p.Voices = []Voice{}
	}
	return &p, nil
}

// PersonKey is a name as it is compared: lower case, single spaces.
func PersonKey(name string) string { return WordKey(name) }

// ListPeople are uid's named voices, by name.
func (s *Store) ListPeople(ctx context.Context, uid int64) ([]Person, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+personCols+` FROM people WHERE user_id = ? ORDER BY name_key`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetPerson is one of uid's people.
func (s *Store) GetPerson(ctx context.Context, uid, id int64) (*Person, error) {
	p, err := scanPerson(s.DB.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE id = ? AND user_id = ?`, id, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// PersonByName finds one of uid's people by name, whatever its capitals.
func (s *Store) PersonByName(ctx context.Context, uid int64, name string) (*Person, error) {
	p, err := scanPerson(s.DB.QueryRowContext(ctx, `SELECT `+personCols+` FROM people WHERE user_id = ? AND name_key = ?`, uid, PersonKey(name)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// CreatePerson saves someone new (ErrConflict when the name is taken).
func (s *Store) CreatePerson(ctx context.Context, uid int64, p *Person) error {
	p.Name = strings.Join(strings.Fields(p.Name), " ")
	if p.Voices == nil {
		p.Voices = []Voice{}
	}
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO people (user_id, name, name_key, voices, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		uid, p.Name, PersonKey(p.Name), toJSON(p.Voices), now, now)
	if err != nil {
		return conflict(err)
	}
	p.ID, _ = res.LastInsertId()
	p.UserID, p.CreatedAt, p.UpdatedAt = uid, now, now
	return nil
}

// SavePerson writes a person's name and voice prints (the newest MaxVoices).
func (s *Store) SavePerson(ctx context.Context, uid int64, p *Person) error {
	p.Name = strings.Join(strings.Fields(p.Name), " ")
	if len(p.Voices) > MaxVoices {
		p.Voices = p.Voices[len(p.Voices)-MaxVoices:]
	}
	p.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE people SET name = ?, name_key = ?, voices = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		p.Name, PersonKey(p.Name), toJSON(p.Voices), p.UpdatedAt, p.ID, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePerson forgets someone and their voice.
func (s *Store) DeletePerson(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM people WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MemosWithPerson are uid's memos that name a person (to rename or forget them there).
func (s *Store) MemosWithPerson(ctx context.Context, uid, id int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.id FROM memos m, json_each(m.speakers) j
		WHERE m.user_id = ? AND (json_extract(j.value, '$.person_id') = ? OR json_extract(j.value, '$.suggest_id') = ?) GROUP BY m.id`, uid, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var mid int64
		if err := rows.Scan(&mid); err != nil {
			return nil, err
		}
		out = append(out, mid)
	}
	return out, rows.Err()
}

// MeetingsWithoutVoices are meetings written out, with their recording, whose
// voices were never looked for (or were being looked for when the app stopped).
func (s *Store) MeetingsWithoutVoices(ctx context.Context) ([]MemoRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id FROM memos
		WHERE meeting = 1 AND status = 'done' AND audio != '' AND (speakers_status = 'working' OR (speakers_status = '' AND speakers = '[]')) ORDER BY id`)
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

// MeetingsWithOldTidy are meetings whose voices are known but whose AI tidy-up
// was written before: it still says "Others:".
func (s *Store) MeetingsWithOldTidy(ctx context.Context) ([]MemoRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id FROM memos WHERE meeting = 1 AND speakers != '[]' AND ai_status != 'working'
		AND (tidy LIKE 'Others:%' OR tidy LIKE '%'||char(10)||'Others:%') ORDER BY id`)
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
