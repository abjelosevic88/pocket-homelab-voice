package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// folderParam turns a folder id from a request ("" or "0" = none) into one
// of uid's folders, or answers 400 and false.
func (s *Server) folderParam(ctx context.Context, w http.ResponseWriter, uid int64, raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "0" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err == nil && id > 0 {
		if _, err = s.store.GetFolder(ctx, uid, id); err == nil {
			return id, true
		}
	}
	writeErr(w, http.StatusBadRequest, "folder_id is one of your folders (GET /folders), or 0 for none")
	return 0, false
}

func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fs, err := s.store.ListFolders(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "folders")
		return
	}
	writeJSON(w, http.StatusOK, fs)
}

func (s *Server) handleGetFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := s.store.GetFolder(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "folder")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func folderName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return "", false
	}
	name := store.CleanFolderName(in.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "a folder needs a name")
		return "", false
	}
	return name, true
}

// handleCreateFolder makes a folder; with a name that is taken it answers
// that folder (200) instead of a second one.
func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	name, ok := folderName(w, r)
	if !ok {
		return
	}
	if f, err := s.store.FolderByName(ctx, uid, name); err == nil {
		writeJSON(w, http.StatusOK, f)
		return
	}
	f, err := s.store.CreateFolder(ctx, uid, name)
	if err != nil {
		s.fail(w, err, "create folder")
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleRenameFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	name, ok := folderName(w, r)
	if !ok {
		return
	}
	id := idParam(r, "id")
	if err := s.store.RenameFolder(ctx, uid, id, name); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "you have a folder called "+strconv.Quote(name)+" already")
			return
		}
		s.fail(w, err, "rename folder")
		return
	}
	f, err := s.store.GetFolder(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "folder")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleDeleteFolder removes a folder; its memos stay, in no folder.
func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteFolder(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete folder")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMoveMemos puts several memos in a folder at once:
// {"ids": [1, 2], "folder_id": 3} (0 = take them out of their folder).
func (s *Server) handleMoveMemos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		IDs      []int64 `json:"ids"`
		FolderID int64   `json:"folder_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 500 {
		writeErr(w, http.StatusBadRequest, "ids lists 1 to 500 memos")
		return
	}
	fid, ok := s.folderParam(ctx, w, uid, strconv.FormatInt(in.FolderID, 10))
	if !ok {
		return
	}
	n, err := s.store.MoveToFolder(ctx, uid, fid, in.IDs)
	if err != nil {
		s.fail(w, err, "move memos")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": n, "folder_id": fid})
}
