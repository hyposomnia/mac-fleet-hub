package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func usePreviewRoot(t *testing.T, root string) {
	t.Helper()
	previous := cfg
	cfg.FileRoot = root
	t.Cleanup(func() { cfg = previous })
}

func TestFilePreviewReturnsMarkdownAndResolvesRelativePaths(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(docs, "plan.md")
	if err := os.WriteFile(path, []byte("# Plan\n\nHello"), 0600); err != nil {
		t.Fatal(err)
	}
	usePreviewRoot(t, root)

	req := httptest.NewRequest(http.MethodGet, "/api/file/preview?path=plan.md&cwd="+urlQueryEscape(docs), nil)
	rr := httptest.NewRecorder()
	handleFilePreview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got filePreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != realPath || got.Name != "plan.md" || got.Kind != "markdown" || got.Content != "# Plan\n\nHello" {
		t.Fatalf("unexpected preview: %+v", got)
	}
	if got.MIME != "text/markdown; charset=utf-8" || got.Size == 0 || got.ModifiedAt == 0 {
		t.Fatalf("missing metadata: %+v", got)
	}
}

func TestFilePreviewSupportsHTMLAndLineSuffix(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.html")
	if err := os.WriteFile(path, []byte("<h1>Preview</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	usePreviewRoot(t, root)

	req := httptest.NewRequest(http.MethodGet, "/api/file/preview?path="+urlQueryEscape(path+":12:4"), nil)
	rr := httptest.NewRecorder()
	handleFilePreview(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got filePreviewResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "html" || got.Line != 12 || got.Column != 4 || got.Content != "<h1>Preview</h1>" {
		t.Fatalf("unexpected preview: %+v", got)
	}
}

func TestFilePreviewRejectsOutsideRootAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	usePreviewRoot(t, root)

	for name, path := range map[string]string{"absolute": secret} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/file/preview?path="+urlQueryEscape(path), nil)
			rr := httptest.NewRecorder()
			handleFilePreview(rr, req)
			if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "file_outside_root") {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(root, "linked.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/file/preview?path="+urlQueryEscape(link), nil)
	rr := httptest.NewRecorder()
	handleFilePreview(rr, req)
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "file_outside_root") {
		t.Fatalf("symlink status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestFilePreviewRejectsUnsupportedAndOversizedText(t *testing.T) {
	root := t.TempDir()
	unsupported := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(unsupported, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(root, "large.md")
	file, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxPreviewTextBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	usePreviewRoot(t, root)

	for name, tc := range map[string]struct {
		path string
		code int
		kind string
	}{
		"unsupported": {unsupported, http.StatusUnsupportedMediaType, "unsupported_file"},
		"large":       {large, http.StatusRequestEntityTooLarge, "preview_too_large"},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/file/preview?path="+urlQueryEscape(tc.path), nil)
			rr := httptest.NewRecorder()
			handleFilePreview(rr, req)
			if rr.Code != tc.code || !strings.Contains(rr.Body.String(), tc.kind) {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestFileContentStreamsRangeAndForcesTextDownloads(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(video, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(root, "page.html")
	if err := os.WriteFile(html, []byte("<script>alert(1)</script>"), 0600); err != nil {
		t.Fatal(err)
	}
	usePreviewRoot(t, root)

	rangeReq := httptest.NewRequest(http.MethodGet, "/api/file/content?path="+urlQueryEscape(video), nil)
	rangeReq.Header.Set("Range", "bytes=2-5")
	rangeRR := httptest.NewRecorder()
	handleFileContent(rangeRR, rangeReq)
	if rangeRR.Code != http.StatusPartialContent || rangeRR.Body.String() != "2345" {
		t.Fatalf("range status=%d body=%q", rangeRR.Code, rangeRR.Body.String())
	}
	if rangeRR.Header().Get("Content-Type") != "video/mp4" || rangeRR.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("range headers=%v", rangeRR.Header())
	}

	inlineReq := httptest.NewRequest(http.MethodGet, "/api/file/content?path="+urlQueryEscape(html), nil)
	inlineRR := httptest.NewRecorder()
	handleFileContent(inlineRR, inlineReq)
	if inlineRR.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("inline html status=%d body=%s", inlineRR.Code, inlineRR.Body.String())
	}

	downloadReq := httptest.NewRequest(http.MethodGet, "/api/file/content?download=1&path="+urlQueryEscape(html), nil)
	downloadRR := httptest.NewRecorder()
	handleFileContent(downloadRR, downloadReq)
	if downloadRR.Code != http.StatusOK || downloadRR.Body.String() != "<script>alert(1)</script>" {
		t.Fatalf("download status=%d body=%q", downloadRR.Code, downloadRR.Body.String())
	}
	if downloadRR.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(downloadRR.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download headers=%v", downloadRR.Header())
	}
}
