package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fileJSONRequest(t *testing.T, method, path string, body interface{}) *http.Request {
	t.Helper()
	var data bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&data).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &data)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestFileListReturnsSafeSortedDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Projects", "zeta"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"README.md":   "# Fleet",
		"archive.bin": "binary",
		".hidden":     "hidden",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
			t.Fatal(err)
		}
	}
	usePreviewRoot(t, root)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/file/list", nil)
	rr := httptest.NewRecorder()
	handleFileList(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got fileBrowserListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Root != realRoot || got.Path != realRoot || got.Parent != "" {
		t.Fatalf("unexpected paths: %+v", got)
	}
	names := make([]string, 0, len(got.Entries))
	for _, entry := range got.Entries {
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "Projects,zeta,.hidden,archive.bin,README.md" {
		t.Fatalf("unexpected entries: %v", names)
	}
	if got.Entries[2].Hidden != true || got.Entries[4].Previewable != true {
		t.Fatalf("entry metadata missing: %+v", got.Entries)
	}
	if len(got.Locations) != 1 || got.Locations[0].ID != "user" || got.Locations[0].Name != "用户" {
		t.Fatalf("unexpected locations: %+v", got.Locations)
	}

	subReq := httptest.NewRequest(http.MethodGet, "/api/file/list?path="+urlQueryEscape(filepath.Join(root, "Projects")), nil)
	subRR := httptest.NewRecorder()
	handleFileList(subRR, subReq)
	if err := json.Unmarshal(subRR.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Parent != realRoot {
		t.Fatalf("subdirectory parent=%q want %q", got.Parent, realRoot)
	}
}

func TestFileBrowserLocationsMergeFinderFavoritesWithMacDefaults(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Desktop", "Documents", "Downloads", "Applications", "Work"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	outsideLink := filepath.Join(root, "Outside")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, outsideLink); err != nil {
			t.Fatal(err)
		}
	}

	favorites := []finderFavorite{
		{Name: "应用程序", Path: filepath.Join(root, "Applications")},
		{Name: "桌面别名", Path: filepath.Join(root, "Desktop")},
		{Name: "Work", Path: filepath.Join(root, "Work")},
		{Name: "Work duplicate", Path: filepath.Join(root, "Work")},
		{Name: "Outside", Path: outside},
	}
	if runtime.GOOS != "windows" {
		favorites = append(favorites, finderFavorite{Name: "Outside link", Path: outsideLink})
	}

	got := mergeFileBrowserLocations(root, favorites)
	names := make([]string, 0, len(got))
	ids := make([]string, 0, len(got))
	for _, location := range got {
		names = append(names, location.Name)
		ids = append(ids, location.ID)
	}
	if want := "桌面,Work,文稿,下载,用户"; strings.Join(names, ",") != want {
		t.Fatalf("location names=%v want %s", names, want)
	}
	if want := "desktop,favorite-1,documents,downloads,user"; strings.Join(ids, ",") != want {
		t.Fatalf("location ids=%v want %s", ids, want)
	}
}

func TestFileBrowserLocationsFallBackToMacDefaults(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}

	got := mergeFileBrowserLocations(root, nil)
	names := make([]string, 0, len(got))
	for _, location := range got {
		names = append(names, location.Name)
	}
	if want := "桌面,文稿,下载,用户"; strings.Join(names, ",") != want {
		t.Fatalf("location names=%v want %s", names, want)
	}
}

func TestFileListRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	usePreviewRoot(t, root)
	req := httptest.NewRequest(http.MethodGet, "/api/file/list?path="+urlQueryEscape(outside), nil)
	rr := httptest.NewRecorder()
	handleFileList(rr, req)
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "file_outside_root") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestFileMutationsStayInsideRoot(t *testing.T) {
	root := t.TempDir()
	usePreviewRoot(t, root)

	mkdirReq := fileJSONRequest(t, http.MethodPost, "/api/file/mkdir", map[string]string{"path": root, "name": "docs"})
	mkdirRR := httptest.NewRecorder()
	handleFileMkdir(mkdirRR, mkdirReq)
	if mkdirRR.Code != http.StatusCreated {
		t.Fatalf("mkdir status=%d body=%s", mkdirRR.Code, mkdirRR.Body.String())
	}
	docs := filepath.Join(root, "docs")
	note := filepath.Join(docs, "note.md")
	if err := os.WriteFile(note, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	renameReq := fileJSONRequest(t, http.MethodPost, "/api/file/rename", map[string]string{"path": note, "name": "plan.md"})
	renameRR := httptest.NewRecorder()
	handleFileRename(renameRR, renameReq)
	if renameRR.Code != http.StatusOK {
		t.Fatalf("rename status=%d body=%s", renameRR.Code, renameRR.Body.String())
	}
	plan := filepath.Join(docs, "plan.md")
	if _, err := os.Stat(plan); err != nil {
		t.Fatal(err)
	}

	deleteReq := fileJSONRequest(t, http.MethodPost, "/api/file/delete", map[string]interface{}{"path": docs, "recursive": false})
	deleteRR := httptest.NewRecorder()
	handleFileDelete(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusConflict {
		t.Fatalf("non-recursive delete status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	deleteReq = fileJSONRequest(t, http.MethodPost, "/api/file/delete", map[string]interface{}{"path": docs, "recursive": true})
	deleteRR = httptest.NewRecorder()
	handleFileDelete(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("recursive delete status=%d body=%s", deleteRR.Code, deleteRR.Body.String())
	}
	if _, err := os.Stat(docs); !os.IsNotExist(err) {
		t.Fatalf("docs still exists: %v", err)
	}

	badReq := fileJSONRequest(t, http.MethodPost, "/api/file/mkdir", map[string]string{"path": root, "name": "../escape"})
	badRR := httptest.NewRecorder()
	handleFileMkdir(badRR, badReq)
	if badRR.Code != http.StatusBadRequest {
		t.Fatalf("traversal status=%d body=%s", badRR.Code, badRR.Body.String())
	}
}

func TestFileUploadCreatesWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	usePreviewRoot(t, root)
	upload := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "note.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("uploaded")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/file/upload?path="+urlQueryEscape(root), &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rr := httptest.NewRecorder()
		handleFileUpload(rr, req)
		return rr
	}

	first := upload()
	if first.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", first.Code, first.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(content) != "uploaded" {
		t.Fatalf("uploaded content=%q err=%v", content, err)
	}
	second := upload()
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "file_exists") {
		t.Fatalf("duplicate status=%d body=%s", second.Code, second.Body.String())
	}
}
