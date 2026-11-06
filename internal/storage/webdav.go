package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Credentials answers the username and password for one request. Asked every
// time, so a key that comes from Pocket Core (and may be switched off there)
// is never kept here.
type Credentials func(ctx context.Context) (user, pass string, err error)

// WebDAV keeps recordings in a folder on a WebDAV server: Pocket Files,
// Nextcloud or any other.
type WebDAV struct {
	// Base is the address of the folder itself.
	Base string
	// Root and Folder say how the folder is made when missing: Folder's
	// segments one by one under Root.
	Root, Folder string
	Creds        Credentials
	Label        string
	UserAgent    string
	kind         string
	// HTTP is for small requests; Big carries recordings (no overall timeout).
	HTTP, Big *http.Client
}

func newWebDAV(kind, root, folder, label string, creds Credentials) (*WebDAV, error) {
	root = strings.TrimRight(strings.TrimSpace(root), "/")
	u, err := url.Parse(root)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("not a WebDAV address: %q", root)
	}
	folder, err = Clean(folder)
	if err != nil {
		return nil, err
	}
	base := root
	if folder != "" {
		base += "/" + escapePath(folder)
	}
	return &WebDAV{Base: base, Root: root, Folder: folder, Creds: creds, Label: label, kind: kind,
		HTTP: &http.Client{Timeout: 60 * time.Second}, Big: &http.Client{}}, nil
}

// NewWebDAV is a folder (may be "") under a WebDAV address.
func NewWebDAV(address, folder string, creds Credentials) (*WebDAV, error) {
	label := "WebDAV · " + hostOf(address)
	if f, _ := Clean(folder); f != "" {
		label += " · " + f
	}
	return newWebDAV("webdav", address, folder, label, creds)
}

// NewNextcloud is a folder inside a Nextcloud user's files.
func NewNextcloud(server, user, folder string, creds Credentials) (*WebDAV, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" || user == "" {
		return nil, errors.New("Nextcloud needs its address and your username")
	}
	f, err := Clean(folder)
	if err != nil {
		return nil, err
	}
	if f == "" {
		return nil, errors.New("Nextcloud needs a folder inside your files")
	}
	return newWebDAV("nextcloud", server+"/remote.php/dav/files/"+url.PathEscape(user), f, "Nextcloud · "+f, creds)
}

// NewPocketFiles is a folder inside the person's Pocket Files, whose WebDAV
// root (davRoot, "<files url>/dav") is their home. The password is a Core
// link token.
func NewPocketFiles(davRoot, folder string, creds Credentials) (*WebDAV, error) {
	f, err := Clean(folder)
	if err != nil {
		return nil, err
	}
	if f == "" {
		return nil, errors.New("Pocket Files needs a folder inside your files, not the whole of them")
	}
	return newWebDAV("files", davRoot, f, "Pocket Files · "+f, creds)
}

func (w *WebDAV) Kind() string     { return w.kind }
func (w *WebDAV) Describe() string { return w.Label }

func hostOf(s string) string {
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Host
	}
	return s
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func (w *WebDAV) urlFor(p string) (string, error) {
	c, err := Clean(p)
	if err != nil || c == "" {
		return "", fmt.Errorf("invalid path %q", p)
	}
	return w.Base + "/" + escapePath(c), nil
}

func (w *WebDAV) do(ctx context.Context, c *http.Client, method, u string, body io.Reader, size int64, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if w.UserAgent != "" {
		req.Header.Set("User-Agent", w.UserAgent)
	}
	if w.Creds != nil {
		user, pass, err := w.Creds(ctx)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(user, pass)
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s cannot be reached: %w", hostOf(w.Root), err)
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		res.Body.Close()
		return nil, fmt.Errorf("%s refused the sign-in (%d)", hostOf(w.Root), res.StatusCode)
	}
	return res, nil
}

func statusErr(what string, res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
	msg := strings.TrimSpace(string(b))
	if len(msg) > 120 || strings.HasPrefix(msg, "<") {
		msg = ""
	}
	if msg != "" {
		return fmt.Errorf("%s: %s (%s)", what, res.Status, msg)
	}
	return fmt.Errorf("%s: %s", what, res.Status)
}

// mkcol makes the folder dir (an address) and answers whether it is there.
func (w *WebDAV) mkcol(ctx context.Context, dir string) error {
	res, err := w.do(ctx, w.HTTP, "MKCOL", dir+"/", nil, -1, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusCreated, http.StatusMethodNotAllowed, http.StatusOK, http.StatusNoContent:
		return nil // made, or already there
	}
	return statusErr("making a folder", res)
}

// mkdirs makes Folder and then rel's folders (rel a relative folder path) under Root.
func (w *WebDAV) mkdirs(ctx context.Context, rel string) error {
	cur := w.Root
	segs := []string{}
	if w.Folder != "" {
		segs = append(segs, strings.Split(w.Folder, "/")...)
	}
	if rel != "" {
		segs = append(segs, strings.Split(rel, "/")...)
	}
	for _, s := range segs {
		cur += "/" + url.PathEscape(s)
		if err := w.mkcol(ctx, cur); err != nil {
			return err
		}
	}
	return nil
}

func (w *WebDAV) Test(ctx context.Context) error {
	if err := w.mkdirs(ctx, ""); err != nil {
		return err
	}
	u := w.Base + "/.pocket-voice-test"
	res, err := w.do(ctx, w.HTTP, http.MethodPut, u, bytes.NewReader([]byte("ok")), 2, nil)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return statusErr("writing there", res)
	}
	if res, err := w.do(ctx, w.HTTP, http.MethodDelete, u, nil, -1, nil); err == nil {
		res.Body.Close()
	}
	return nil
}

func (w *WebDAV) Put(ctx context.Context, p, src string) error {
	c, err := Clean(p)
	if err != nil || c == "" {
		return fmt.Errorf("invalid path %q", p)
	}
	dir := ""
	if i := strings.LastIndex(c, "/"); i > 0 {
		dir = c[:i]
	}
	if err := w.mkdirs(ctx, dir); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	u, _ := w.urlFor(c)
	res, err := w.do(ctx, w.Big, http.MethodPut, u, f, fi.Size(), nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return statusErr("copying the recording", res)
	}
	return nil
}

func (w *WebDAV) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	u, err := w.urlFor(p)
	if err != nil {
		return nil, err
	}
	res, err := w.do(ctx, w.Big, http.MethodGet, u, nil, -1, nil)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		res.Body.Close()
		return nil, ErrNotFound
	}
	if res.StatusCode >= 300 {
		defer res.Body.Close()
		return nil, statusErr("reading the recording", res)
	}
	return res.Body, nil
}

func (w *WebDAV) Fetch(ctx context.Context, p, dst string) error {
	body, err := w.Open(ctx, p)
	if err != nil {
		return err
	}
	defer body.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, body); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// Serve passes the player's request on (Range included) and the answer back.
func (w *WebDAV) Serve(rw http.ResponseWriter, r *http.Request, p, contentType string) error {
	u, err := w.urlFor(p)
	if err != nil {
		return err
	}
	hdr := http.Header{}
	for _, k := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := r.Header.Get(k); v != "" {
			hdr.Set(k, v)
		}
	}
	res, err := w.do(r.Context(), w.Big, http.MethodGet, u, nil, -1, hdr)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 400 && res.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return statusErr("reading the recording", res)
	}
	for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := res.Header.Get(k); v != "" {
			rw.Header().Set(k, v)
		}
	}
	if contentType != "" {
		rw.Header().Set("Content-Type", contentType)
	} else if v := res.Header.Get("Content-Type"); v != "" {
		rw.Header().Set("Content-Type", v)
	}
	if rw.Header().Get("Accept-Ranges") == "" {
		rw.Header().Set("Accept-Ranges", "bytes")
	}
	rw.WriteHeader(res.StatusCode)
	_, _ = io.Copy(rw, res.Body)
	return nil
}

func (w *WebDAV) Delete(ctx context.Context, p string) error {
	u, err := w.urlFor(p)
	if err != nil {
		return err
	}
	res, err := w.do(ctx, w.HTTP, http.MethodDelete, u, nil, -1, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotFound {
		return statusErr("removing the recording", res)
	}
	return nil
}
