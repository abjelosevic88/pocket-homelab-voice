package server

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

func TestFolders(t *testing.T) {
	a := newApp(t, nil)
	f := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/folders", map[string]any{"name": "  Project   Atlas "})
	if f["name"] != "Project Atlas" {
		t.Fatalf("folder: %v", f)
	}
	fid := num(f, "id")
	// the same name (any case) is that folder, not a second one
	if again := a.expect(http.StatusOK, a.alice, "POST", "/api/v1/folders", map[string]any{"name": "project atlas"}); num(again, "id") != fid {
		t.Fatalf("second folder with the same name: %v", again)
	}
	a.expect(http.StatusBadRequest, a.alice, "POST", "/api/v1/folders", map[string]any{"name": "  "})

	in := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "Kick-off.", "folder_id": fid})
	if num(in, "folder_id") != fid {
		t.Fatalf("made inside the folder: %v", in)
	}
	out := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "Elsewhere."})
	third := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/memos", map[string]any{"text": "Later."})

	// someone else's folder is not one of yours
	bobs := a.expect(http.StatusCreated, a.bob, "POST", "/api/v1/folders", map[string]any{"name": "Bob's"})
	a.expect(http.StatusBadRequest, a.alice, "PUT", "/api/v1/memos/"+id(out), map[string]any{"folder_id": num(bobs, "id")})
	a.expect(http.StatusNotFound, a.bob, "PUT", "/api/v1/folders/"+itoa(int64(fid)), map[string]any{"name": "mine"})
	a.expect(http.StatusNotFound, a.bob, "DELETE", "/api/v1/folders/"+itoa(int64(fid)), nil)
	if l := a.list(a.bob, "/api/v1/folders"); len(l) != 1 {
		t.Fatalf("Bob's folders: %v", l)
	}

	// move one, and two at once
	if m := a.ok(a.alice, "PUT", "/api/v1/memos/"+id(out), map[string]any{"folder_id": fid}); num(m, "folder_id") != fid {
		t.Fatalf("moved: %v", m)
	}
	a.ok(a.alice, "POST", "/api/v1/memos/folder", map[string]any{"ids": []any{num(third, "id")}, "folder_id": fid})
	if l := a.ok(a.alice, "GET", "/api/v1/memos?folder="+itoa(int64(fid)), nil); num(l, "total") != 3 {
		t.Fatalf("in the folder: %v", l)
	}
	// other changes keep the folder
	a.ok(a.alice, "PUT", "/api/v1/memos/"+id(third), map[string]any{"title": "Renamed", "starred": true})
	if m := a.ok(a.alice, "GET", "/api/v1/memos/"+id(third), nil); num(m, "folder_id") != fid {
		t.Fatalf("a title change lost the folder: %v", m)
	}
	a.ok(a.alice, "POST", "/api/v1/memos/folder", map[string]any{"ids": []any{num(third, "id")}, "folder_id": 0})
	if l := a.ok(a.alice, "GET", "/api/v1/memos?folder=none", nil); num(l, "total") != 1 {
		t.Fatalf("in no folder: %v", l)
	}
	fs := a.list(a.alice, "/api/v1/folders")
	if len(fs) != 1 || num(fs[0].(map[string]any), "count") != 2 {
		t.Fatalf("folders with counts: %v", fs)
	}

	// export → import elsewhere keeps the folder
	req, _ := http.NewRequest("GET", a.url+"/api/v1/export.json", nil)
	req.Header.Set("Authorization", "Bearer "+a.alice)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := io.ReadAll(res.Body)
	res.Body.Close()
	req, _ = http.NewRequest("POST", a.url+"/api/v1/import", bytes.NewReader(doc))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.bob)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("import: %v", err)
	}
	var atlas map[string]any
	for _, x := range a.list(a.bob, "/api/v1/folders") {
		if x.(map[string]any)["name"] == "Project Atlas" {
			atlas = x.(map[string]any)
		}
	}
	if atlas == nil || num(atlas, "count") != 2 {
		t.Fatalf("Bob's imported folder: %v", a.list(a.bob, "/api/v1/folders"))
	}

	// rename; a taken name is refused; removing keeps the memos
	other := a.expect(http.StatusCreated, a.alice, "POST", "/api/v1/folders", map[string]any{"name": "Other"})
	a.expect(http.StatusConflict, a.alice, "PUT", "/api/v1/folders/"+id(other), map[string]any{"name": "PROJECT ATLAS"})
	if r := a.ok(a.alice, "PUT", "/api/v1/folders/"+itoa(int64(fid)), map[string]any{"name": "Atlas"}); r["name"] != "Atlas" {
		t.Fatalf("rename: %v", r)
	}
	a.expect(http.StatusNoContent, a.alice, "DELETE", "/api/v1/folders/"+itoa(int64(fid)), nil)
	if l := a.ok(a.alice, "GET", "/api/v1/memos?folder=none", nil); num(l, "total") != 3 {
		t.Fatalf("memos after the folder went: %v", l)
	}
	if m := a.ok(a.alice, "GET", "/api/v1/memos/"+id(in), nil); num(m, "folder_id") != 0 {
		t.Fatalf("memo still points at a removed folder: %v", m)
	}
}
