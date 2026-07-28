package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxFileUploadBytes int64 = 512 << 20

type fileBrowserEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Extension   string `json:"extension,omitempty"`
	MIME        string `json:"mime,omitempty"`
	Size        int64  `json:"size"`
	ModifiedAt  int64  `json:"modifiedAt"`
	Hidden      bool   `json:"hidden,omitempty"`
	Symlink     bool   `json:"symlink,omitempty"`
	Previewable bool   `json:"previewable,omitempty"`
}

type fileBrowserLocation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type fileBrowserListResponse struct {
	Root      string                `json:"root"`
	Path      string                `json:"path"`
	Parent    string                `json:"parent,omitempty"`
	Entries   []fileBrowserEntry    `json:"entries"`
	Locations []fileBrowserLocation `json:"locations"`
}

func fileBrowserRoot() (string, string, error) {
	root := cfg.FileRoot
	if root == "" {
		root, _ = os.UserHomeDir()
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", errPreviewOutsideRoot
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", errPreviewOutsideRoot
	}
	return rootAbs, rootReal, nil
}

func resolveFileBrowserPath(rawPath string) (string, error) {
	rootAbs, rootReal, err := fileBrowserRoot()
	if err != nil {
		return "", err
	}
	rawPath = strings.TrimSpace(rawPath)
	if len(rawPath) > 16*1024 || strings.ContainsRune(rawPath, '\x00') {
		return "", fmt.Errorf("invalid file path")
	}
	if rawPath == "" {
		return rootReal, nil
	}

	candidate := rawPath
	if candidate == "~" {
		candidate, _ = os.UserHomeDir()
	} else if strings.HasPrefix(candidate, "~/") {
		home, _ := os.UserHomeDir()
		candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
	} else if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootReal, candidate)
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

// Mutations verify the real parent but keep the final path lexical so a symlink
// can be rejected instead of accidentally mutating its target.
func resolveFileMutationPath(rawPath string) (string, error) {
	rootAbs, rootReal, err := fileBrowserRoot()
	if err != nil {
		return "", err
	}
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" || len(rawPath) > 16*1024 || strings.ContainsRune(rawPath, '\x00') {
		return "", fmt.Errorf("invalid file path")
	}
	candidate := rawPath
	if candidate == "~" {
		candidate, _ = os.UserHomeDir()
	} else if strings.HasPrefix(candidate, "~/") {
		home, _ := os.UserHomeDir()
		candidate = filepath.Join(home, strings.TrimPrefix(candidate, "~/"))
	} else if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootReal, candidate)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil || (!pathWithinRoot(rootAbs, candidateAbs) && !pathWithinRoot(rootReal, candidateAbs)) {
		return "", errPreviewOutsideRoot
	}
	parentReal, err := filepath.EvalSymlinks(filepath.Dir(candidateAbs))
	if err != nil || !pathWithinRoot(rootReal, parentReal) {
		return "", errPreviewOutsideRoot
	}
	target := filepath.Join(parentReal, filepath.Base(candidateAbs))
	if target == rootReal {
		return "", errPreviewOutsideRoot
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errPreviewNotFound
		}
		return "", errPreviewNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errPreviewOutsideRoot
	}
	return target, nil
}

func validFileLeaf(name string) bool {
	return name != "" && name != "." && name != ".." && len([]byte(name)) <= 255 &&
		filepath.Base(name) == name && !strings.ContainsAny(name, "/\\") && !strings.ContainsRune(name, '\x00')
}

func fileBrowserEntryFor(dir string, entry os.DirEntry) (fileBrowserEntry, bool) {
	path := filepath.Join(dir, entry.Name())
	symlink := entry.Type()&os.ModeSymlink != 0
	info, err := entry.Info()
	if symlink {
		realPath, resolveErr := resolveFileBrowserPath(path)
		if resolveErr != nil {
			return fileBrowserEntry{}, false
		}
		info, err = os.Stat(realPath)
	}
	if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return fileBrowserEntry{}, false
	}
	kind := "file"
	size := info.Size()
	if info.IsDir() {
		kind = "folder"
		size = 0
	}
	extension := strings.ToLower(filepath.Ext(entry.Name()))
	format, previewable := previewFormatForPath(path)
	previewable = previewable && format.Kind != "stylesheet"
	return fileBrowserEntry{
		Name: entry.Name(), Path: path, Kind: kind, Extension: extension,
		MIME: mime.TypeByExtension(extension), Size: size, ModifiedAt: info.ModTime().UnixMilli(),
		Hidden: strings.HasPrefix(entry.Name(), "."), Symlink: symlink,
		Previewable: kind == "file" && previewable,
	}, true
}

func fileBrowserLocations(root string) []fileBrowserLocation {
	locations := []fileBrowserLocation{{ID: "home", Name: "主目录", Path: root}}
	for _, location := range []fileBrowserLocation{
		{ID: "projects", Name: "项目", Path: filepath.Join(root, "Projects")},
		{ID: "downloads", Name: "下载", Path: filepath.Join(root, "Downloads")},
		{ID: "desktop", Name: "桌面", Path: filepath.Join(root, "Desktop")},
	} {
		info, err := os.Stat(location.Path)
		if err == nil && info.IsDir() {
			locations = append(locations, location)
		}
	}
	return locations
}

func handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path, err := resolveFileBrowserPath(r.URL.Query().Get("path"))
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		writeErr(w, http.StatusBadRequest, "not_a_directory", "所选路径不是文件夹。")
		return
	}
	dirEntries, err := os.ReadDir(path)
	if err != nil {
		writeErr(w, http.StatusForbidden, "directory_unreadable", "无法读取这个文件夹。")
		return
	}
	entries := make([]fileBrowserEntry, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if item, ok := fileBrowserEntryFor(path, entry); ok {
			entries = append(entries, item)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind == "folder"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	_, root, _ := fileBrowserRoot()
	parent := ""
	if path != root {
		parent = filepath.Dir(path)
		if !pathWithinRoot(root, parent) {
			parent = ""
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, fileBrowserListResponse{
		Root: root, Path: path, Parent: parent, Entries: entries, Locations: fileBrowserLocations(root),
	})
}

func handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || !validFileLeaf(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid_name", "文件夹名称无效。")
		return
	}
	parent, err := resolveFileBrowserPath(req.Path)
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		writeErr(w, http.StatusBadRequest, "not_a_directory", "目标路径不是文件夹。")
		return
	}
	target := filepath.Join(parent, req.Name)
	if err := os.Mkdir(target, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeErr(w, http.StatusConflict, "file_exists", "同名文件或文件夹已经存在。")
			return
		}
		writeErr(w, http.StatusInternalServerError, "mkdir_failed", "新建文件夹失败。")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"path": target})
}

func handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parent, err := resolveFileBrowserPath(r.URL.Query().Get("path"))
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		writeErr(w, http.StatusBadRequest, "not_a_directory", "目标路径不是文件夹。")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFileUploadBytes+(1<<20))
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_upload", "没有收到要上传的文件。")
		return
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if !validFileLeaf(name) {
		writeErr(w, http.StatusBadRequest, "invalid_name", "文件名无效。")
		return
	}
	target := filepath.Join(parent, name)
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			writeErr(w, http.StatusConflict, "file_exists", "同名文件已经存在。")
			return
		}
		writeErr(w, http.StatusInternalServerError, "upload_failed", "无法创建上传文件。")
		return
	}
	written, copyErr := io.Copy(out, io.LimitReader(file, maxFileUploadBytes+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written > maxFileUploadBytes {
		_ = os.Remove(target)
		if written > maxFileUploadBytes {
			writeErr(w, http.StatusRequestEntityTooLarge, "upload_too_large", "单个文件不能超过 512 MB。")
			return
		}
		writeErr(w, http.StatusInternalServerError, "upload_failed", "上传文件失败。")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"path": target, "size": written})
}

func handleFileRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil || !validFileLeaf(req.Name) {
		writeErr(w, http.StatusBadRequest, "invalid_name", "新名称无效。")
		return
	}
	source, err := resolveFileMutationPath(req.Path)
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	target := filepath.Join(filepath.Dir(source), req.Name)
	if _, err := os.Lstat(target); err == nil {
		writeErr(w, http.StatusConflict, "file_exists", "同名文件或文件夹已经存在。")
		return
	} else if !os.IsNotExist(err) {
		writeErr(w, http.StatusInternalServerError, "rename_failed", "无法检查目标名称。")
		return
	}
	if err := os.Rename(source, target); err != nil {
		writeErr(w, http.StatusInternalServerError, "rename_failed", "重命名失败。")
		return
	}
	writeJSON(w, map[string]string{"path": target})
}

func handleFileDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "invalid_path", "文件路径无效。")
		return
	}
	target, err := resolveFileMutationPath(req.Path)
	if err != nil {
		writePreviewPathError(w, err)
		return
	}
	info, err := os.Lstat(target)
	if err != nil {
		writePreviewPathError(w, errPreviewNotFound)
		return
	}
	if info.IsDir() && req.Recursive {
		err = os.RemoveAll(target)
	} else {
		err = os.Remove(target)
	}
	if err != nil {
		if info.IsDir() && !req.Recursive {
			writeErr(w, http.StatusConflict, "directory_not_empty", "文件夹不为空，需要确认后递归删除。")
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete_failed", "删除失败。")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
