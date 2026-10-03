package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDesktopAutostartOnlyBootstrapsPrivateManagedDefinition(t *testing.T) {
	directory := desktopTestDirectory(t)
	definition := filepath.Join(directory, "agent.plist")
	if err := os.WriteFile(definition, []byte("definition"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	launch := func(args ...string) error {
		calls = append(calls, args)
		if args[0] == "print" {
			return errors.New("not loaded")
		}
		return nil
	}
	if err := desktopAutostartStart(directory, launch); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !reflect.DeepEqual(calls[2], []string{"bootstrap", svcDomain(), definition}) {
		t.Fatalf("%v", calls)
	}
	os.Chmod(definition, 0644)
	calls = nil
	if desktopAutostartStart(directory, launch) == nil || len(calls) != 0 {
		t.Fatal("bootstrapped unsafe definition")
	}
}

func TestDesktopAutostartLeavesExistingLegacyServiceAlone(t *testing.T) {
	directory := desktopTestDirectory(t)
	os.WriteFile(filepath.Join(directory, "agent.plist"), []byte("definition"), 0600)
	calls := 0
	if desktopAutostartStart(directory, func(args ...string) error { calls++; return nil }) == nil || calls != 1 {
		t.Fatal("started second Fleet alongside legacy service")
	}
}
