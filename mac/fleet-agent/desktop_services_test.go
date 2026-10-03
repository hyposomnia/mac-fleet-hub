package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopFilesInitializationFailureCanRetryWithoutPartialDatabase(t *testing.T) {
	state := desktopTestDirectory(t)
	binding := deviceBinding{DeviceID: "m12", Index: "12", AgentPort: 7682, TerminalPort: 7681, FilesPort: 8080}
	commands, err := desktopServiceCommands(binding, "/bundle/bin", state, "/Users/fixture")
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	err = desktopInitializeFiles(commands[:3], state, func(command desktopServiceCommand) error {
		runs++
		if runs == 2 {
			return errors.New("interrupted user creation")
		}
		return os.WriteFile(command.Args[1], []byte("initializing"), 0600)
	})
	if err == nil {
		t.Fatal("ignored interrupted initialization")
	}
	if _, err := os.Stat(filepath.Join(state, "files.db")); !os.IsNotExist(err) {
		t.Fatal("published partial database")
	}
	entries, _ := os.ReadDir(state)
	if len(entries) != 0 {
		t.Fatal("left failed staging file")
	}
	runs = 0
	err = desktopInitializeFiles(commands[:3], state, func(command desktopServiceCommand) error {
		runs++
		return os.WriteFile(command.Args[1], []byte("ready"), 0600)
	})
	if err != nil || runs != 3 {
		t.Fatalf("retry skipped user initialization: %v, runs=%d", err, runs)
	}
	runs = 0
	err = desktopInitializeFiles(commands[:3], state, func(command desktopServiceCommand) error { runs++; return nil })
	if err != nil || runs != 1 {
		t.Fatal("reinitialized an existing database")
	}
}

func TestDesktopServicesStayOnLoopbackAndUseBundledExecutables(t *testing.T) {
	binding := deviceBinding{DeviceID: "m12", Index: "12", AgentPort: 7682, TerminalPort: 7681, FilesPort: 8080}
	commands, err := desktopServiceCommands(binding, "/Applications/Fleet Hub.app/bin", "/Users/fixture/private", "/Users/fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 5 {
		t.Fatalf("%+v", commands)
	}
	for _, command := range commands {
		if !strings.HasPrefix(command.Program, "/Applications/Fleet Hub.app/bin/") {
			t.Fatal("used external runtime dependency")
		}
		if strings.Contains(strings.Join(command.Args, " "), "sudo") {
			t.Fatal("terminal privilege escalation in GUI setup")
		}
	}
	terminal := commands[4]
	if !strings.Contains(strings.Join(terminal.Args, " "), "127.0.0.1") || !strings.Contains(strings.Join(terminal.Args, " "), "/m12/term") {
		t.Fatalf("%+v", terminal)
	}
	files := commands[3]
	if !strings.Contains(strings.Join(files.Args, " "), "127.0.0.1") || !strings.Contains(strings.Join(files.Args, " "), "/m12/files") {
		t.Fatalf("%+v", files)
	}
	binding.FilesPort = binding.TerminalPort
	if _, err := desktopServiceCommands(binding, "bin", "state", "home"); err == nil {
		t.Fatal("accepted conflicting service ports")
	}
}
