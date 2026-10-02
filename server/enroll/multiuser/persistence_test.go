package multiuser

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestIncorrectEncryptionKeyRefusesExistingDatabase(t *testing.T) {
	state := t.TempDir()
	options := Options{StateDir: state, Origin: "http://127.0.0.1:7099", Key: bytes.Repeat([]byte{1}, 32)}
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	registerBrowser(t, server, server.now(), "key-identity@example.com")
	server.Close()
	options.Key = bytes.Repeat([]byte{2}, 32)
	server, err = New(options)
	if err == nil {
		server.Close()
		t.Fatal("wrong key accepted an existing encrypted identity")
	}
}

func TestMissingEncryptionKeyDoesNotReplaceExistingIdentity(t *testing.T) {
	state := t.TempDir()
	options := Options{StateDir: state, Origin: "http://127.0.0.1:7099"}
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	keyFile := filepath.Join(state, "encryption.key")
	original, err := os.ReadFile(keyFile)
	if err != nil || len(original) != 32 {
		t.Fatalf("key length=%d %v", len(original), err)
	}
	if err = os.Rename(keyFile, keyFile+".backup"); err != nil {
		t.Fatal(err)
	}
	server, err = New(options)
	if err == nil {
		server.Close()
		t.Fatal("missing key silently generated a replacement for an existing database")
	}
	if _, err = os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatalf("replacement key created %v", err)
	}
}
