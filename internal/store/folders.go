package store

import (
	"context"
	"strings"
)

// Folder is one of the person's containers for memos.
type Folder struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	Name      string `json:"name"`
	Count     int    `json:"count"`   // memos in it
	Inbox     int    `json:"inbox"`   // of those, not dealt with yet
	LastAt    string `json:"last_at"` // the newest memo's recorded_at ("" = empty)
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// FolderKey is a folder name as it is compared.
func FolderKey(name string) string { return WordKey(name) }

// CleanFolderName is a name as it is kept: one line, single spaces, at most 80 letters.
func CleanFolderName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 80 {
		name = strings.TrimSpace(string(r[:80]))
	}
	return name
}

const folderSelect = `SELECT f.id, f.user_id, f.name, f.created_at, f.updated_at,
	COUNT(m.id), COALESCE(SUM(m.filed = 0), 0), COALESCE(MAX(m.recorded_at), '')
	FROM folders f LEFT JOIN memos m ON m.folder_id = f.id AND m.user_id = f.user_id`

func scanFolder(r scanner) (*Folder, error) {
	var f Folder
	if err := r.Scan(&f.ID, &f.UserID, &f.Name, &f.CreatedAt, &f.UpdatedAt, &f.Count, &f.Inbox, &f.LastAt); err != nil {
		return nil, err
	}
	return &f, nil
}

// ListFolders are uid's folders by name, with how many memos each holds.
func (s *Store) ListFolders(ctx context.Context, uid int64) ([]Folder, error) {
	rows, err := s.DB.QueryContext(ctx, folderSelect+` WHERE f.user_id = ? GROUP BY f.id ORDER BY f.name_key`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// GetFolder reads one of uid's folders.
func (s *Store) GetFolder(ctx context.Context, uid, id int64) (*Folder, error) {
	f, err := scanFolder(s.DB.QueryRowContext(ctx, folderSelect+` WHERE f.user_id = ? AND f.id = ? GROUP BY f.id`, uid, id))
	return f, notFound(err)
}

// FolderByName finds uid's folder with this name (any case).
func (s *Store) FolderByName(ctx context.Context, uid int64, name string) (*Folder, error) {
	f, err := scanFolder(s.DB.QueryRowContext(ctx, folderSelect+` WHERE f.user_id = ? AND f.name_key = ? GROUP BY f.id`, uid, FolderKey(name)))
	return f, notFound(err)
}

// CreateFolder saves a new folder (ErrConflict when the name is taken).
func (s *Store) CreateFolder(ctx context.Context, uid int64, name string) (*Folder, error) {
	name = CleanFolderName(name)
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO folders (user_id, name, name_key, created_at, updated_at) VALUES (?,?,?,?,?)`, uid, name, FolderKey(name), now, now)
	if err != nil {
		return nil, conflict(err)
	}
	id, _ := res.LastInsertId()
	return &Folder{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

// RenameFolder gives one of uid's folders a new name (ErrConflict when taken).
func (s *Store) RenameFolder(ctx context.Context, uid, id int64, name string) error {
	name = CleanFolderName(name)
	res, err := s.DB.ExecContext(ctx, `UPDATE folders SET name = ?, name_key = ?, updated_at = ? WHERE id = ? AND user_id = ?`, name, FolderKey(name), Now(), id, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteFolder removes one of uid's folders; its memos stay, in no folder.
func (s *Store) DeleteFolder(ctx context.Context, uid, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memos SET folder_id = 0 WHERE folder_id = ? AND user_id = ?`, id, uid); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveToFolder puts several of uid's memos in a folder (0 = in none) and
// says how many moved.
func (s *Store) MoveToFolder(ctx context.Context, uid, folderID int64, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := make([]string, len(ids))
	args := []any{folderID, Now(), uid}
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE memos SET folder_id = ?, updated_at = ? WHERE user_id = ? AND id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
