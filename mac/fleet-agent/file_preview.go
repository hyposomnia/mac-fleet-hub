package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxPreviewTextBytes int64 = 5 << 20

var (
	errPreviewOutsideRoot = errors.New("preview path is outside file root")
	errPreviewNotFound    = errors.New("preview file not found")
	previewLineSuffix     = regexp.MustCompile(`^(.*?):(\d+)(?::(\d+))?$`)
	previewFormats        = map[string]previewFormat{
		".md":       {Kind: "markdown", MIME: "text/markdown; charset=utf-8", Text: true},
		".markdown": {Kind: "markdown", MIME: "text/markdown; charset=utf-8", Text: true},
		".html":     {Kind: "html", MIME: "text/html; charset=utf-8", Text: true},
		".htm":      {Kind: "html", MIME: "text/html; charset=utf-8", Text: true},
		".txt":      {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".log":      {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".json":     {Kind: "text", MIME: "application/json; charset=utf-8", Text: true},
		".jsonl":    {Kind: "text", MIME: "application/x-ndjson; charset=utf-8", Text: true},
		".js":       {Kind: "text", MIME: "text/javascript; charset=utf-8", Text: true},
		".mjs":      {Kind: "text", MIME: "text/javascript; charset=utf-8", Text: true},
		".cjs":      {Kind: "text", MIME: "text/javascript; charset=utf-8", Text: true},
		".ts":       {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".tsx":      {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".jsx":      {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".go":       {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".py":       {Kind: "text", MIME: "text/x-python; charset=utf-8", Text: true},
		".rb":       {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".sh":       {Kind: "text", MIME: "text/x-shellscript; charset=utf-8", Text: true},
		".zsh":      {Kind: "text", MIME: "text/x-shellscript; charset=utf-8", Text: true},
		".yaml":     {Kind: "text", MIME: "application/yaml; charset=utf-8", Text: true},
		".yml":      {Kind: "text", MIME: "application/yaml; charset=utf-8", Text: true},
		".toml":     {Kind: "text", MIME: "application/toml; charset=utf-8", Text: true},
		".xml":      {Kind: "text", MIME: "application/xml; charset=utf-8", Text: true},
		".csv":      {Kind: "text", MIME: "text/csv; charset=utf-8", Text: true},
		".ini":      {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".conf":     {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".env":      {Kind: "text", MIME: "text/plain; charset=utf-8", Text: true},
		".css":      {Kind: "text", MIME: "text/css; charset=utf-8", Text: true, Stream: true},
		".png":      {Kind: "image", MIME: "image/png", Stream: true},
		".jpg":      {Kind: "image", MIME: "image/jpeg", Stream: true},
		".jpeg":     {Kind: "image", MIME: "image/jpeg", Stream: true},
		".gif":      {Kind: "image", MIME: "image/gif", Stream: true},
		".webp":     {Kind: "image", MIME: "image/webp", Stream: true},
		".avif":     {Kind: "image", MIME: "image/avif", Stream: true},
		".svg":      {Kind: "image", MIME: "image/svg+xml", Stream: true},
		".heic":     {Kind: "image", MIME: "image/heic", Stream: true},
		".heif":     {Kind: "image", MIME: "image/heif", Stream: true},
		".mp4":      {Kind: "video", MIME: "video/mp4", Stream: true},
		".m4v":      {Kind: "video", MIME: "video/x-m4v", Stream: true},
		".webm":     {Kind: "video", MIME: "video/webm", Stream: true},
		".mov":      {Kind: "video", MIME: "video/quicktime", Stream: true},
		".ogv":      {Kind: "video", MIME: "video/ogg", Stream: true},
		".mpeg":     {Kind: "video", MIME: "video/mpeg", Stream: true},
		".mpg":      {Kind: "video", MIME: "video/mpeg", Stream: true},
		".mp3":      {Kind: "audio", MIME: "audio/mpeg", Stream: true},
		".m4a":      {Kind: "audio", MIME: "audio/mp4", Stream: true},
		".aac":      {Kind: "audio", MIME: "audio/aac", Stream: true},
		".wav":      {Kind: "audio", MIME: "audio/wav", Stream: true},
		".ogg":      {Kind: "audio", MIME: "audio/ogg", Stream: true},
		".flac":     {Kind: "audio", MIME: "audio/flac", Stream: true},
		".opus":     {Kind: "audio", MIME: "audio/opus", Stream: true},
		".aif":      {Kind: "audio", MIME: "audio/aiff", Stream: true},
		".aiff":     {Kind: "audio", MIME: "audio/aiff", Stream: true},
		".vtt":      {Kind: "track", MIME: "text/vtt; charset=utf-8", Stream: true},
		".pdf":      {Kind: "pdf", MIME: "application/pdf", Stream: true},
	}
)

type previewFormat struct {
	Kind   string
	MIME   string
	Text   bool
	Stream bool
}

type filePreviewResponse struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	ModifiedAt int64  `json:"modifiedAt"`
	Content    string `json:"content,omitempty"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
}

func handleFilePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path, line, column, err := resolvePreviewRequestPath(r.URL.Query().Get("path"), r.URL.Query().Get("cwd"))
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	format, ok := previewFormatForPath(path)
	if !ok {
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_file", "暂不支持预览这种文件格式。")
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "file_not_found", "文件不存在或不是普通文件。")
		return
	}

	response := filePreviewResponse{
		Path: path, Name: filepath.Base(path), Kind: format.Kind, MIME: format.MIME,
		Size: info.Size(), ModifiedAt: info.ModTime().UnixMilli(), Line: line, Column: column,
	}
	if format.Text {
		if info.Size() > maxPreviewTextBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "preview_too_large", "文本文件超过 5 MB，无法直接预览。")
			return
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			writeErr(w, http.StatusNotFound, "file_not_found", "无法读取文件。")
			return
		}
		content, readErr := io.ReadAll(io.LimitReader(file, maxPreviewTextBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			writeErr(w, http.StatusNotFound, "file_not_found", "无法读取文件。")
			return
		}
		if closeErr != nil {
			writeErr(w, http.StatusInternalServerError, "file_read_failed", "读取文件失败。")
			return
		}
		if int64(len(content)) > maxPreviewTextBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "preview_too_large", "文本文件超过 5 MB，无法直接预览。")
			return
		}
		if !utf8.Valid(content) {
			writeErr(w, http.StatusUnsupportedMediaType, "invalid_text_encoding", "文本文件不是 UTF-8 编码。")
			return
		}
		response.Content = string(content)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, response)
}

func handleFileContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path, _, _, err := resolvePreviewRequestPath(r.URL.Query().Get("path"), r.URL.Query().Get("cwd"))
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	format, ok := previewFormatForPath(path)
	download := r.URL.Query().Get("download") == "1"
	if !download && (!ok || !format.Stream) {
		writeErr(w, http.StatusUnsupportedMediaType, "unsupported_file", "该文件不能作为预览资源输出。")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file_not_found", "文件不存在。")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "file_not_found", "文件不存在或不是普通文件。")
		return
	}
	if strings.EqualFold(filepath.Ext(path), ".css") && info.Size() > maxPreviewTextBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "preview_too_large", "样式文件超过 5 MB，无法加载。")
		return
	}

	disposition := "inline"
	contentType := format.MIME
	if !ok {
		contentType = "application/octet-stream"
	}
	if download {
		disposition = "attachment"
		contentType = "application/octet-stream"
	}
	if value := mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(path)}); value != "" {
		w.Header().Set("Content-Disposition", value)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.EqualFold(filepath.Ext(path), ".css") {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func previewFormatForPath(path string) (previewFormat, bool) {
	format, ok := previewFormats[strings.ToLower(filepath.Ext(path))]
	return format, ok
}

func resolvePreviewRequestPath(rawPath, cwd string) (string, int, int, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" || len(rawPath) > 16*1024 || strings.ContainsRune(rawPath, '\x00') {
		return "", 0, 0, fmt.Errorf("invalid preview path")
	}
	resolved, err := resolvePreviewPath(rawPath, cwd)
	if err == nil {
		return resolved, 0, 0, nil
	}
	if !errors.Is(err, errPreviewNotFound) {
		return "", 0, 0, err
	}
	match := previewLineSuffix.FindStringSubmatch(rawPath)
	if match == nil || match[1] == "" {
		return "", 0, 0, err
	}
	resolved, retryErr := resolvePreviewPath(match[1], cwd)
	if retryErr != nil {
		return "", 0, 0, retryErr
	}
	line, _ := strconv.Atoi(match[2])
	column := 0
	if match[3] != "" {
		column, _ = strconv.Atoi(match[3])
	}
	return resolved, line, column, nil
}

func resolvePreviewPath(rawPath, cwd string) (string, error) {
	root := cfg.FileRoot
	if root == "" {
		root, _ = os.UserHomeDir()
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", errPreviewOutsideRoot
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", errPreviewOutsideRoot
	}

	candidate := rawPath
	if candidate == "~" {
		candidate, _ = os.UserHomeDir()
	} else if strings.HasPrefix(candidate, "~/") {
		home, _ := os.UserHomeDir()
		candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
	} else if !filepath.IsAbs(candidate) {
		base := cwd
		if base == "" {
			base = rootReal
		}
		candidate = filepath.Join(base, candidate)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil || (!pathWithinRoot(rootAbs, candidateAbs) && !pathWithinRoot(rootReal, candidateAbs)) {
		return "", errPreviewOutsideRoot
	}
	candidateReal, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errPreviewNotFound
		}
		return "", errPreviewNotFound
	}
	if !pathWithinRoot(rootReal, candidateReal) {
		return "", errPreviewOutsideRoot
	}
	return candidateReal, nil
}

func pathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writePreviewPathError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errPreviewOutsideRoot):
		writeErr(w, http.StatusForbidden, "file_outside_root", "该路径不在 Fleet 允许预览的目录内。")
	case errors.Is(err, errPreviewNotFound):
		writeErr(w, http.StatusNotFound, "file_not_found", "文件不存在。")
	default:
		writeErr(w, http.StatusBadRequest, "invalid_path", "文件路径无效。")
	}
}
