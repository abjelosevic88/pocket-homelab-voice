// Package storage keeps recordings somewhere other than this box's data
// folder: a server folder the admin offers (a NAS mount), the person's Pocket
// Files, or any WebDAV server (Nextcloud included). Recordings always arrive
// and are written out here first; once done they move to the chosen place and
// are streamed from there (with seeking) when played.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound is a recording that is not where it should be.
var ErrNotFound = errors.New("the recording is not there")

// Store is one place recordings are kept. Paths are relative, with slashes.
type Store interface {
	// Kind is folder | files | webdav | nextcloud.
	Kind() string
	// Describe says where it is, for people ("NAS · /mnt/nas/pocket-voice/Ana").
	Describe() string
	// Test checks the place can be written (making the folder when missing).
	Test(ctx context.Context) error
	// Put copies the local file src to p, making folders on the way.
	Put(ctx context.Context, p, src string) error
	// Fetch copies p to the local file dst.
	Fetch(ctx context.Context, p, dst string) error
	// Serve answers r with p's bytes, Range requests included.
	Serve(w http.ResponseWriter, r *http.Request, p, contentType string) error
	// Open reads p from the start.
	Open(ctx context.Context, p string) (io.ReadCloser, error)
	// Delete removes p; a missing one is fine.
	Delete(ctx context.Context, p string) error
}

// Clean normalises a relative path: forward slashes, no leading slash, no
// "." or ".." parts.
func Clean(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	var parts []string
	for _, s := range strings.Split(p, "/") {
		switch s {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("invalid path %q", p)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "/"), nil
}

// FileName is a recording's name where people browse it: the day and time,
// the title and the memo number (which keeps it unique), e.g.
// "2026/11/2026-11-06 12.00 Daily Team Meeting (10).m4a".
func FileName(recorded time.Time, title string, id int64, ext string) string {
	t := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t':
			return ' '
		}
		return r
	}, title)
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > 60 {
		t = strings.TrimSpace(string(r[:60]))
	}
	t = strings.TrimLeft(t, ".")
	name := recorded.Format("2006-01-02 15.04")
	if t != "" {
		name += " " + t
	}
	return path.Join(recorded.Format("2006/01"), fmt.Sprintf("%s (%d)%s", name, id, ext))
}

// ---- a folder on this server ----

// Folder keeps recordings in a folder the box's owner made available.
type Folder struct {
	Root  string
	Label string
}

func (f *Folder) Kind() string     { return "folder" }
func (f *Folder) Describe() string { return f.Label }

func (f *Folder) abs(p string) (string, error) {
	c, err := Clean(p)
	if err != nil || c == "" {
		return "", fmt.Errorf("invalid path %q", p)
	}
	return filepath.Join(f.Root, filepath.FromSlash(c)), nil
}

// Local is where p is on this server's disk, for programs that read files.
func (f *Folder) Local(p string) (string, error) { return f.abs(p) }

func (f *Folder) Test(ctx context.Context) error {
	if err := os.MkdirAll(f.Root, 0o755); err != nil {
		return fmt.Errorf("the folder cannot be made: %w", err)
	}
	t, err := os.CreateTemp(f.Root, ".pocket-voice-test-*")
	if err != nil {
		return fmt.Errorf("the folder cannot be written: %w", err)
	}
	name := t.Name()
	t.Close()
	return os.Remove(name)
}

func (f *Folder) Put(ctx context.Context, p, src string) error {
	a, err := f.abs(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a), 0o755); err != nil {
		return err
	}
	return copyFile(src, a)
}

func (f *Folder) Fetch(ctx context.Context, p, dst string) error {
	a, err := f.abs(p)
	if err != nil {
		return err
	}
	if err := copyFile(a, dst); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (f *Folder) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	a, err := f.abs(p)
	if err != nil {
		return nil, err
	}
	r, err := os.Open(a)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return r, err
}

func (f *Folder) Serve(w http.ResponseWriter, r *http.Request, p, contentType string) error {
	a, err := f.abs(p)
	if err != nil {
		return err
	}
	file, err := os.Open(a)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		return err
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, "", fi.ModTime(), file)
	return nil
}

func (f *Folder) Delete(ctx context.Context, p string) error {
	a, err := f.abs(p)
	if err != nil {
		return err
	}
	if err := os.Remove(a); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// leave no empty month folders behind
	for d := filepath.Dir(a); d != f.Root && strings.HasPrefix(d, f.Root); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break
		}
	}
	return nil
}

// copyFile copies src to dst through a temporary file and a rename, so a
// half-copied recording never has the real name.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".pv-tmp-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, 0o644)
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
