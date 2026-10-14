package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileNavigationOutsideHomeAndParent(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	usePreviewRoot(t, home)
	for _, path := range []string{home, outside} {
		rr := httptest.NewRecorder()
		handleFileList(rr, httptest.NewRequest("GET", "/api/file/list?path="+urlQueryEscape(path), nil))
		var got fileBrowserListResponse
		json.Unmarshal(rr.Body.Bytes(), &got)
		real, _ := filepath.EvalSymlinks(path)
		if rr.Code != 200 || got.Root != "/" || got.Parent != filepath.Dir(real) {
			t.Fatalf("navigation: %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestFilePreviewCommonEncoding(t *testing.T) {
	usePreviewRoot(t, t.TempDir())
	for _, tc := range []struct {
		name        string
		data        []byte
		query, want string
	}{
		{"utf16", []byte{0xff, 0xfe, 0x60, 0x4f, 0x7d, 0x59}, "", "你好"},
		{"gbk", []byte{0xc4, 0xe3, 0xba, 0xc3}, "", "你好"},
		{"western", []byte{'c', 'a', 'f', 0xe9}, "&encoding=windows-1252", "café"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(cfg.FileRoot, tc.name+".txt")
			os.WriteFile(path, tc.data, 0600)
			rr := httptest.NewRecorder()
			handleFilePreview(rr, httptest.NewRequest("GET", "/api/file/preview?path="+urlQueryEscape(path)+tc.query, nil))
			var got filePreviewResponse
			json.Unmarshal(rr.Body.Bytes(), &got)
			if rr.Code != 200 || got.Content != tc.want {
				t.Fatalf("%d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestFilePreviewSizeBoundaryAndOriginalDownload(t *testing.T) {
	usePreviewRoot(t, t.TempDir())
	path := filepath.Join(cfg.FileRoot, "large.txt")
	content := bytes.Repeat([]byte("a"), int(maxPreviewTextBytes))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	preview := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		handleFilePreview(rr, httptest.NewRequest("GET", "/api/file/preview?path="+urlQueryEscape(path), nil))
		return rr
	}
	if rr := preview(); rr.Code != 200 {
		t.Fatalf("at limit: %d", rr.Code)
	}
	content = append(content, 'b')
	os.WriteFile(path, content, 0600)
	if rr := preview(); rr.Code != 413 || !strings.Contains(rr.Body.String(), "文件过大，不支持预览") {
		t.Fatalf("above limit: %d %s", rr.Code, rr.Body.String())
	}
	rr := httptest.NewRecorder()
	handleFileContent(rr, httptest.NewRequest("GET", "/api/file/content?download=1&path="+urlQueryEscape(path), nil))
	if rr.Code != 200 || !bytes.Equal(rr.Body.Bytes(), content) || !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("download did not preserve oversized original")
	}
}

func TestFileMutationsOutsideHomeKeepOverwriteAndSymlinkGuards(t *testing.T) {
	usePreviewRoot(t, t.TempDir())
	outside := t.TempDir()
	source := filepath.Join(outside, "note.txt")
	target := filepath.Join(outside, "taken.txt")
	os.WriteFile(source, []byte("source"), 0600)
	os.WriteFile(target, []byte("original"), 0600)
	rr := httptest.NewRecorder()
	handleFileRename(rr, fileJSONRequest(t, "POST", "/api/file/rename", map[string]string{"path": source, "name": "taken.txt"}))
	if rr.Code != 409 {
		t.Fatalf("overwrite guard: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	handleFileRename(rr, fileJSONRequest(t, "POST", "/api/file/rename", map[string]string{"path": source, "name": "new.txt"}))
	if rr.Code != 200 {
		t.Fatalf("outside rename: %d %s", rr.Code, rr.Body.String())
	}
	link := filepath.Join(outside, "link")
	os.Symlink(target, link)
	rr = httptest.NewRecorder()
	handleFileDelete(rr, fileJSONRequest(t, "POST", "/api/file/delete", map[string]string{"path": link}))
	if rr.Code != 403 {
		t.Fatalf("symlink guard: %d", rr.Code)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "original" {
		t.Fatal("target changed")
	}
}

func TestOversizedStylesheetStillDownloadsOriginal(t *testing.T) {
	usePreviewRoot(t, t.TempDir())
	path := filepath.Join(cfg.FileRoot, "large.css")
	content := bytes.Repeat([]byte("a"), int(maxPreviewTextBytes)+1)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	for _, download := range []bool{false, true} {
		query := "/api/file/content?path=" + urlQueryEscape(path)
		if download {
			query += "&download=1"
		}
		response := httptest.NewRecorder()
		handleFileContent(response, httptest.NewRequest("GET", query, nil))
		if download {
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), content) {
				t.Fatalf("original download blocked: %d", response.Code)
			}
		} else if response.Code != 413 {
			t.Fatalf("oversized inline stylesheet accepted: %d", response.Code)
		}
	}
}
