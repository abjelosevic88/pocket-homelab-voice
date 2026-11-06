package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/storage"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- where recordings are kept ----
//
// Every recording arrives here (DATA_DIR/audio) and is written out from here.
// Once done, it moves to the place the person chose: a server folder the
// admin offers (STORAGE_ROOTS, e.g. a NAS mount), their Pocket Files (through
// a Core link), Nextcloud or any WebDAV server. It is played from there with
// seeking, and fetched back for a moment when it is written out again.
// Choosing another place moves every recording over; choosing this box
// brings them back.

// filesScopes is what Voice asks Pocket Files for, through a Core app link.
var filesScopes = []string{"files:read", "files:write"}

// filesUse is the manifest's Use for Pocket Files.
func filesUse() core.Use {
	return core.Use{App: "files", Scopes: filesScopes, Reason: "Keep your recordings in a folder of your Pocket Files (Settings → Where recordings are kept), when you choose it"}
}

// placeKey names a person's chosen place; "" is this box. A recording moved
// there carries the key, so a later change of place is noticed.
func placeKey(st store.Settings) string {
	switch st.StorageKind {
	case "folder":
		return "folder:" + filepath.Clean(st.StoragePath)
	case "files":
		f, _ := storage.Clean(st.StoragePath)
		return "files:" + f
	case "webdav":
		f, _ := storage.Clean(st.StoragePath)
		return "webdav:" + st.StorageURL + "|" + st.StorageUser + "|" + f
	case "nextcloud":
		f, _ := storage.Clean(st.StoragePath)
		return "nextcloud:" + st.StorageURL + "|" + st.StorageUser + "|" + f
	}
	return ""
}

// allowedFolder reports whether p is one of STORAGE_ROOTS or inside one.
func (s *Server) allowedFolder(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	p = filepath.Clean(p)
	for _, r := range s.cfg.StorageRoots {
		r = filepath.Clean(r)
		if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// placeFor opens the person's chosen place; nil (and no error) is this box.
func (s *Server) placeFor(ctx context.Context, uid int64, st store.Settings) (storage.Store, error) {
	ua := s.cfg.UserAgent
	password := func(context.Context) (string, string, error) { return st.StorageUser, st.StoragePass, nil }
	switch st.StorageKind {
	case "", "box":
		return nil, nil
	case "folder":
		if !s.allowedFolder(st.StoragePath) {
			return nil, fmt.Errorf("the folder %q is not one this server offers (STORAGE_ROOTS)", st.StoragePath)
		}
		p := filepath.Clean(st.StoragePath)
		return &storage.Folder{Root: p, Label: "Server folder · " + p}, nil
	case "webdav":
		w, err := storage.NewWebDAV(st.StorageURL, st.StoragePath, password)
		if err != nil {
			return nil, err
		}
		w.UserAgent = ua
		return w, nil
	case "nextcloud":
		w, err := storage.NewNextcloud(st.StorageURL, st.StorageUser, st.StoragePath, password)
		if err != nil {
			return nil, err
		}
		w.UserAgent = ua
		return w, nil
	case "files":
		if !s.core.Configured() {
			return nil, errors.New("there is no Pocket Core on this box; Pocket Files is reached through it")
		}
		u, err := s.store.GetUser(ctx, uid)
		if err != nil || u.Email == "" {
			return nil, errors.New("Pocket Files needs your email to ask Pocket Core for you")
		}
		lt, err := s.core.ActAs(ctx, u.Email, "files", filesScopes...)
		if err != nil {
			return nil, filesLinkErr(err)
		}
		if lt.URL == "" {
			return nil, errors.New("Pocket Core gave no address for Pocket Files")
		}
		w, err := storage.NewPocketFiles(strings.TrimRight(lt.URL, "/")+"/dav", st.StoragePath, s.filesCreds(u.Email, lt))
		if err != nil {
			return nil, err
		}
		w.UserAgent = ua
		return w, nil
	}
	return nil, fmt.Errorf("unknown place %q", st.StorageKind)
}

// filesLinkErr words Core refusing the Voice → Files link like the other links.
func filesLinkErr(err error) error {
	var le *core.LinkError
	if errors.As(err, &le) {
		return errors.New("Pocket Files through Pocket Core: " + firstNonEmpty(linkReason("Files", le.Reason), le.Message))
	}
	return err
}

// filesCreds answers the WebDAV sign-in for one person's Pocket Files: a
// link token Core issued for them, used for four minutes and then asked for
// anew (Core re-checks the switches each time).
func (s *Server) filesCreds(email string, first *core.LinkToken) storage.Credentials {
	var mu sync.Mutex
	lt, at := first, time.Now()
	return func(ctx context.Context) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		if lt != nil && time.Since(at) < 4*time.Minute {
			return "pocket-voice", lt.AccessToken, nil
		}
		fresh, err := s.core.ActAs(ctx, email, "files", filesScopes...)
		if err != nil {
			lt = nil
			return "", "", filesLinkErr(err)
		}
		lt, at = fresh, time.Now()
		return "pocket-voice", lt.AccessToken, nil
	}
}

// ---- one person's move ----

// moveState is a person's recordings being moved to their place.
type moveState struct {
	Running bool   `json:"running"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Failed  int    `json:"failed"`
	Error   string `json:"error,omitempty"`
	At      string `json:"at,omitempty"` // when it last ran
	again   bool
	// from is the place recordings are leaving (a person who switched from one
	// place to another); recordings there are played from it meanwhile.
	from    storage.Store
	fromKey string
}

func (s *Server) moveOf(uid int64) moveState {
	s.movesMu.Lock()
	defer s.movesMu.Unlock()
	if mv := s.moves[uid]; mv != nil {
		return *mv
	}
	return moveState{}
}

// storeOf is where m's recording is: nil for this box.
func (s *Server) storeOf(ctx context.Context, m *store.Memo) (storage.Store, error) {
	if m.AudioAt == "" {
		return nil, nil
	}
	s.movesMu.Lock()
	if mv := s.moves[m.UserID]; mv != nil && mv.from != nil && mv.fromKey == m.AudioAt {
		from := mv.from
		s.movesMu.Unlock()
		return from, nil
	}
	s.movesMu.Unlock()
	st, err := s.store.GetSettings(ctx, m.UserID)
	if err != nil {
		return nil, err
	}
	if placeKey(st) != m.AudioAt {
		return nil, errors.New("this recording is kept in a place you no longer use; it is being moved")
	}
	return s.placeFor(ctx, m.UserID, st)
}

// localAudio is a file on this server with m's recording, for ffmpeg and
// whisper; done removes it when it was fetched for the purpose.
func (s *Server) localAudio(ctx context.Context, m *store.Memo) (string, func(), error) {
	if fresh, err := s.store.GetMemo(ctx, m.UserID, m.ID); err == nil {
		m.AudioAt, m.AudioAway = fresh.AudioAt, fresh.AudioAway
	}
	if m.AudioAt == "" {
		p := s.audioPath(m.UserID, m.Audio)
		if _, err := os.Stat(p); err != nil {
			return "", nil, errors.New("the recording file is missing")
		}
		return p, func() {}, nil
	}
	st, err := s.storeOf(ctx, m)
	if err != nil {
		return "", nil, err
	}
	if f, ok := st.(*storage.Folder); ok {
		p, err := f.Local(m.AudioAway)
		if err == nil {
			if _, err = os.Stat(p); err == nil {
				return p, func() {}, nil
			}
		}
		return "", nil, errors.New("the recording file is missing from " + f.Describe())
	}
	tmp := filepath.Join(s.tmpDir(), fmt.Sprintf("fetch-%d-%d%s", m.ID, time.Now().UnixNano(), filepath.Ext(m.Audio)))
	if err := st.Fetch(ctx, m.AudioAway, tmp); err != nil {
		return "", nil, fmt.Errorf("the recording could not be fetched from %s: %w", st.Describe(), err)
	}
	return tmp, func() { _ = os.Remove(tmp) }, nil
}

// openAudio reads m's recording from the start (exports).
func (s *Server) openAudio(ctx context.Context, m *store.Memo) (io.ReadCloser, error) {
	st, err := s.storeOf(ctx, m)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return os.Open(s.audioPath(m.UserID, m.Audio))
	}
	return st.Open(ctx, m.AudioAway)
}

// removeAudio lets go of m's recording wherever it is.
func (s *Server) removeAudio(ctx context.Context, m *store.Memo) {
	if m.Audio == "" {
		return
	}
	if m.AudioAt == "" {
		_ = os.Remove(s.audioPath(m.UserID, m.Audio))
		return
	}
	st, err := s.storeOf(ctx, m)
	if err == nil {
		err = st.Delete(ctx, m.AudioAway)
	}
	if err != nil {
		s.log.Warn("storage: remove recording", "memo", m.ID, "err", err)
	}
	_ = s.store.SetAudioAway(ctx, m.UserID, m.ID, "", "")
}

// keepAway moves uid's finished recordings to their place (or back to this
// box) in the background; one move per person at a time.
func (s *Server) keepAway(uid int64) {
	s.movesMu.Lock()
	mv := s.moves[uid]
	if mv == nil {
		mv = &moveState{}
		s.moves[uid] = mv
	}
	if mv.Running {
		mv.again = true
		s.movesMu.Unlock()
		return
	}
	mv.Running, mv.again, mv.Done, mv.Total, mv.Failed, mv.Error = true, false, 0, 0, 0, ""
	s.movesMu.Unlock()
	go func() {
		for {
			err := s.moveAll(s.bg, uid, mv)
			s.movesMu.Lock()
			if err != nil {
				mv.Error = err.Error()
			}
			mv.At = store.Now()
			if mv.again && s.bg.Err() == nil {
				mv.again = false
				s.movesMu.Unlock()
				continue
			}
			mv.Running = false
			if err == nil && mv.Failed == 0 {
				mv.from, mv.fromKey = nil, "" // nothing is left at the old place
			}
			s.movesMu.Unlock()
			return
		}
	}()
}

func (s *Server) moveAll(ctx context.Context, uid int64, mv *moveState) error {
	st, err := s.store.GetSettings(ctx, uid)
	if err != nil {
		return err
	}
	key := placeKey(st)
	to, err := s.placeFor(ctx, uid, st)
	if err != nil {
		return err
	}
	if to != nil {
		if err := to.Test(ctx); err != nil {
			return fmt.Errorf("%s cannot be written: %w", to.Describe(), err)
		}
	}
	skip := map[int64]bool{}
	for ctx.Err() == nil {
		batch, err := s.store.AudioToMove(ctx, uid, key, 200)
		if err != nil {
			return err
		}
		var todo []store.Memo
		for _, m := range batch {
			if !skip[m.ID] {
				todo = append(todo, m)
			}
		}
		if len(todo) == 0 {
			return nil
		}
		s.movesMu.Lock()
		if mv.Total < mv.Done+mv.Failed+len(todo) {
			mv.Total = mv.Done + mv.Failed + len(todo)
		}
		s.movesMu.Unlock()
		for i := range todo {
			m := &todo[i]
			err := s.moveOne(ctx, m, to, key)
			s.movesMu.Lock()
			if err != nil {
				skip[m.ID] = true
				mv.Failed++
				mv.Error = firstNonEmpty(m.Title, "a memo") + ": " + err.Error()
			} else {
				mv.Done++
			}
			s.movesMu.Unlock()
			if err != nil {
				s.log.Warn("storage: move", "memo", m.ID, "err", err)
				if ctx.Err() == nil && errors.Is(err, errPlaceDown) {
					return err // the place is not answering: try again later
				}
			}
		}
	}
	return ctx.Err()
}

var errPlaceDown = errors.New("the place is not answering")

// moveOne moves one recording to `to` (nil = this box) under key.
func (s *Server) moveOne(ctx context.Context, m *store.Memo, to storage.Store, key string) error {
	src, done, err := s.localAudio(ctx, m)
	if err != nil {
		return err
	}
	defer done()
	from, _ := s.storeOf(ctx, m)
	if to == nil {
		dst := s.audioPath(m.UserID, m.Audio)
		if src != dst {
			if err := os.MkdirAll(s.audioDir(m.UserID), 0o755); err != nil {
				return err
			}
			if err := copyLocal(src, dst); err != nil {
				return err
			}
		}
		if err := s.store.SetAudioAway(ctx, m.UserID, m.ID, "", ""); err != nil {
			return err
		}
	} else {
		when, err := time.Parse(time.RFC3339Nano, m.RecordedAt)
		if err != nil {
			when = time.Now()
		}
		name := storage.FileName(when.Local(), m.Title, m.ID, strings.ToLower(filepath.Ext(m.Audio)))
		if err := to.Put(ctx, name, src); err != nil {
			return fmt.Errorf("%w: %v", errPlaceDown, err)
		}
		if err := s.store.SetAudioAway(ctx, m.UserID, m.ID, key, name); err != nil {
			_ = to.Delete(ctx, name)
			return err
		}
	}
	// the copy is safe: let go of the one it came from
	switch {
	case m.AudioAt == "":
		if to != nil {
			_ = os.Remove(s.audioPath(m.UserID, m.Audio))
		}
	case from != nil:
		if err := from.Delete(ctx, m.AudioAway); err != nil {
			s.log.Warn("storage: remove the old copy", "memo", m.ID, "err", err)
		}
	}
	return nil
}

func copyLocal(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst + ".part")
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst + ".part")
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(dst+".part", dst)
}

// sweepStorage moves, for everyone with a place, what is not there yet
// (new recordings, and ones the place did not take last time).
func (s *Server) sweepStorage(ctx context.Context) {
	ids, err := s.store.UsersWithAudio(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil {
			continue
		}
		key := placeKey(st)
		places := s.store.AudioPlaces(ctx, uid)
		for at := range places {
			if at != key {
				s.keepAway(uid)
				break
			}
		}
	}
}

// ---- API ----

// storageOption is one place a person may choose.
type storageOption struct {
	Kind  string   `json:"kind"`
	Name  string   `json:"name"`
	Ready bool     `json:"ready"`
	Why   string   `json:"why,omitempty"`
	Roots []string `json:"roots,omitempty"`
}

// handleGetStorage says where the caller's recordings are kept, how many are
// where, the move under way and the places on offer.
func (s *Server) handleGetStorage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	key := placeKey(st)
	where := "This server"
	if p, err := s.placeFor(ctx, u.ID, st); err == nil && p != nil {
		where = p.Describe()
	}
	places := s.store.AudioPlaces(ctx, u.ID)
	here, there, elsewhere := places[""], store.AudioPlace{}, store.AudioPlace{}
	for at, p := range places {
		switch {
		case at == "":
		case at == key:
			there = p
		default:
			elsewhere.Count += p.Count
			elsewhere.Bytes += p.Bytes
		}
	}
	if key == "" {
		there, here = here, store.AudioPlace{}
	}
	filesReady, filesWhy := s.core.Configured(), ""
	if !filesReady {
		filesWhy = "there is no Pocket Core on this box"
	}
	opts := []storageOption{
		{Kind: "box", Name: "This server", Ready: true},
		{Kind: "folder", Name: "A server folder", Ready: len(s.cfg.StorageRoots) > 0, Roots: s.cfg.StorageRoots,
			Why: map[bool]string{true: "", false: "the admin has not offered any (STORAGE_ROOTS)"}[len(s.cfg.StorageRoots) > 0]},
		{Kind: "files", Name: "Pocket Files", Ready: filesReady, Why: filesWhy},
		{Kind: "nextcloud", Name: "Nextcloud", Ready: true},
		{Kind: "webdav", Name: "WebDAV", Ready: true},
	}
	user := ""
	if u.Name != "" {
		user = strings.Fields(u.Name)[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": firstNonEmpty(st.StorageKind, "box"), "path": st.StoragePath, "url": st.StorageURL, "user": st.StorageUser,
		"password": mask(st.StoragePass), "where": where,
		"kept_there": there, "waiting_here": here, "elsewhere": elsewhere,
		"move": s.moveOf(u.ID), "options": opts, "suggested_folder": firstNonEmpty(user, "Voice"),
	})
}

// handlePutStorage chooses where the caller's recordings are kept: it checks
// the place can be written, saves it and starts moving every recording there.
func (s *Server) handlePutStorage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Kind     string `json:"kind"`
		Path     string `json:"path"`
		URL      string `json:"url"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if s.cfg.DemoData {
		writeErr(w, http.StatusConflict, "a box that shows demo data keeps its recordings here")
		return
	}
	cur, err := s.store.GetSettings(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	next := cur
	next.StorageKind = strings.TrimSpace(in.Kind)
	if next.StorageKind == "box" {
		next.StorageKind = ""
	}
	next.StoragePath, next.StorageURL, next.StorageUser = strings.TrimSpace(in.Path), strings.TrimRight(strings.TrimSpace(in.URL), "/"), strings.TrimSpace(in.User)
	if !strings.Contains(in.Password, "••") {
		next.StoragePass = strings.TrimSpace(in.Password)
	}
	switch next.StorageKind {
	case "":
		next.StoragePath, next.StorageURL, next.StorageUser, next.StoragePass = "", "", "", ""
	case "folder":
		next.StoragePath = filepath.Clean(next.StoragePath)
		next.StorageURL, next.StorageUser, next.StoragePass = "", "", ""
	case "files":
		next.StorageURL, next.StorageUser, next.StoragePass = "", "", ""
		if next.StoragePath == "" {
			next.StoragePath = "Voice"
		}
	case "nextcloud", "webdav":
	default:
		writeErr(w, http.StatusBadRequest, "choose this server, a server folder, Pocket Files, Nextcloud or WebDAV")
		return
	}
	newKey, oldKey := placeKey(next), placeKey(cur)
	to, err := s.placeFor(ctx, u.ID, next)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if to != nil {
		tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := to.Test(tctx)
		cancel()
		if err != nil {
			writeErr(w, http.StatusBadRequest, to.Describe()+" cannot be written: "+err.Error())
			return
		}
	}
	// recordings at the old place stay reachable through it while they move
	var from storage.Store
	if oldKey != "" && oldKey != newKey {
		if from, err = s.placeFor(ctx, u.ID, cur); err != nil {
			s.log.Warn("storage: the old place", "user", u.ID, "err", err)
		}
	}
	if err := s.store.SaveUserSettings(ctx, u.ID, next); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if from != nil {
		s.movesMu.Lock()
		mv := s.moves[u.ID]
		if mv == nil {
			mv = &moveState{}
			s.moves[u.ID] = mv
		}
		mv.from, mv.fromKey = from, oldKey
		s.movesMu.Unlock()
	}
	s.log.Info("storage: place chosen", "user", u.ID, "kind", firstNonEmpty(next.StorageKind, "box"))
	s.keepAway(u.ID)
	s.handleGetStorage(w, r)
}

// handleAudioFrom serves a recording kept elsewhere; false when it is here.
func (s *Server) serveAway(w http.ResponseWriter, r *http.Request, m *store.Memo) bool {
	if m.AudioAt == "" {
		return false
	}
	st, err := s.storeOf(r.Context(), m)
	if err == nil {
		err = st.Serve(w, r, m.AudioAway, firstNonEmpty(m.AudioType, "application/octet-stream"))
	}
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "the recording file is missing")
		} else {
			writeErr(w, http.StatusBadGateway, err.Error())
		}
	}
	return true
}
