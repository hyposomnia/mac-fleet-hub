package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChooseCodexAppToolsPipeUsesOldestOwnedSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mfh-app-tools-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	older := filepath.Join(dir, "older.sock")
	newer := filepath.Join(dir, "newer.sock")
	oldListener, err := net.Listen("unix", older)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oldListener.Close() })
	newListener, err := net.Listen("unix", newer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = newListener.Close() })
	if err := os.Chmod(older, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(newer, 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	got, err := chooseCodexAppToolsPipe([]string{newer, older, newer})
	if err != nil {
		t.Fatal(err)
	}
	if got != older {
		t.Fatalf("pipe got %q want %q", got, older)
	}
}

func TestChooseCodexAppToolsPipeRejectsRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := chooseCodexAppToolsPipe([]string{path}); err == nil {
		t.Fatal("expected regular file to be rejected")
	}
}

func TestChooseCodexAppToolsPipeRejectsUnprotectedSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mfh-app-tools-mode-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "browser.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := chooseCodexAppToolsPipe([]string{path}); err == nil {
		t.Fatal("expected 0755 browser socket to be rejected")
	}
}
