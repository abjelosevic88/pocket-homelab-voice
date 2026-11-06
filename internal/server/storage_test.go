package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/config"
)

// fakeDAV is the little WebDAV a recording needs: folders, put, get (with
// Range), delete, behind a password.
func fakeDAV(t *testing.T) (*httptest.Server, string) {
	root := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "ana" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/dav")))
		switch r.Method {
		case "MKCOL":
			if _, err := os.Stat(p); err == nil {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if os.Mkdir(p, 0o755) != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			if os.WriteFile(p, b, 0o644) != nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			http.ServeFile(w, r, p)
		case http.MethodDelete:
			if os.Remove(p) != nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, root
}

func (a *app) waitMoved(where string) map[string]any {
	a.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := a.ok(a.alice, "GET", "/api/v1/storage", nil)
		mv, _ := st["move"].(map[string]any)
		there, _ := st["kept_there"].(map[string]any)
		if mv["running"] == false && num(there, "count") == 1 && strings.Contains(st["where"].(string), where) {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	a.t.Fatalf("recordings did not move to %s: %v", where, a.ok(a.alice, "GET", "/api/v1/storage", nil))
	return nil
}

func (a *app) audio(memo, rng string) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest("GET", a.url+"/api/v1/memos/"+memo+"/audio", nil)
	req.Header.Set("Authorization", "Bearer "+a.alice)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func findFiles(t *testing.T, root string) []string {
	var out []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestRecordingsMoveToTheChosenPlace(t *testing.T) {
	nas := t.TempDir()
	tools := fakeTools(t, "Sastanak u devet.|Kupi kafu.", "bs")
	a := newApp(t, func(c *config.Config) {
		tools(c)
		c.StorageRoots = []string{nas}
	})
	wav := testWAV()
	code, m := a.upload(a.alice, map[string]string{"recorded_at": "2026-11-06T12:00:00+01:00"}, "meeting.wav", wav)
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %v", code, m)
	}
	memo := id(m)
	a.waitDone(a.alice, memo)
	local := findFiles(t, a.s.cfg.AudioDir)
	if len(local) != 1 {
		t.Fatalf("the recording is on this box first: %v", local)
	}

	// a folder the admin did not offer is refused
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "folder", "path": "/etc"})

	// the NAS folder: the recording moves there under a readable name
	a.ok(a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "folder", "path": filepath.Join(nas, "Alice")})
	a.waitMoved("Server folder")
	there := findFiles(t, filepath.Join(nas, "Alice"))
	if len(there) != 1 || !strings.Contains(filepath.ToSlash(there[0]), "/2026/11/2026-11-06 12.00 ") || !strings.HasSuffix(there[0], " ("+memo+").m4a") {
		t.Fatalf("on the NAS: %v", there)
	}
	if left := findFiles(t, a.s.cfg.AudioDir); len(left) != 0 {
		t.Fatalf("the box copy is let go: %v", left)
	}
	if code, b := a.audio(memo, "bytes=0-9"); code != http.StatusPartialContent || !bytes.Equal(b, wav[:10]) {
		t.Fatalf("played from the NAS with seeking: %d %d bytes", code, len(b))
	}
	// written out again from there
	a.ok(a.alice, "POST", "/api/v1/memos/"+memo+"/retry", map[string]any{})
	if d := a.waitDone(a.alice, memo); d["status"] != "done" {
		t.Fatalf("written out again from the NAS: %v", d)
	}

	// WebDAV (Nextcloud, Pocket Files): moved over, the NAS copy let go
	dav, davRoot := fakeDAV(t)
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "webdav", "url": dav.URL + "/dav", "user": "ana", "password": "wrong", "path": "Voice"})
	a.ok(a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "webdav", "url": dav.URL + "/dav", "user": "ana", "password": "secret", "path": "Voice"})
	a.waitMoved("WebDAV")
	if got := findFiles(t, filepath.Join(davRoot, "Voice")); len(got) != 1 {
		t.Fatalf("on the WebDAV server: %v", got)
	}
	if left := findFiles(t, filepath.Join(nas, "Alice")); len(left) != 0 {
		t.Fatalf("the NAS copy is let go: %v", left)
	}
	if code, b := a.audio(memo, "bytes=4-7"); code != http.StatusPartialContent || !bytes.Equal(b, wav[4:8]) {
		t.Fatalf("played through WebDAV with seeking: %d %q", code, b)
	}
	st := a.ok(a.alice, "GET", "/api/v1/storage", nil)
	if st["password"] == "secret" {
		t.Fatal("the password is shown")
	}
	// saving with the masked password keeps it
	a.ok(a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "webdav", "url": dav.URL + "/dav", "user": "ana", "password": st["password"], "path": "Voice"})

	// back to this box
	a.ok(a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "box"})
	a.waitMoved("This server")
	if got := findFiles(t, a.s.cfg.AudioDir); len(got) != 1 {
		t.Fatalf("back on the box: %v", got)
	}
	if code, b := a.audio(memo, ""); code != 200 || !bytes.Equal(b, wav) {
		t.Fatalf("played from the box: %d", code)
	}
	if got := findFiles(t, filepath.Join(davRoot, "Voice")); len(got) != 0 {
		t.Fatalf("the WebDAV copy is let go: %v", got)
	}

	// deleting a memo kept away removes its recording there too
	a.ok(a.alice, "PUT", "/api/v1/storage", map[string]any{"kind": "folder", "path": filepath.Join(nas, "Alice")})
	a.waitMoved("Server folder")
	a.expect(http.StatusNoContent, a.alice, "DELETE", "/api/v1/memos/"+memo, nil)
	if left := findFiles(t, filepath.Join(nas, "Alice")); len(left) != 0 {
		t.Fatalf("deleted memo left its recording: %v", left)
	}
	// Bob's place is his own
	if st := a.ok(a.bob, "GET", "/api/v1/storage", nil); st["kind"] != "box" {
		t.Fatalf("bob: %v", st)
	}
}
