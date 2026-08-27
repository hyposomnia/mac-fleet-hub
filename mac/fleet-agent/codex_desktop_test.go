package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func launchctlArgs(operations []codexDesktopLaunchctlOperation) [][]string {
	if operations == nil {
		return nil
	}
	args := make([][]string, 0, len(operations))
	for _, operation := range operations {
		args = append(args, operation.Args)
	}
	return args
}

func TestCodexDesktopLaunchctlOperations(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		config    Config
		want      [][]string
		wantError string
	}{
		{
			name:   "shared defaults to loopback websocket",
			goos:   "darwin",
			config: Config{CodexMode: "shared", CodexDesktopShare: true},
			want: [][]string{
				{"unsetenv", codexDesktopLocalDaemonEnv},
				{"setenv", codexDesktopWebSocketEnv, codexSharedWebSocketEndpoint},
			},
		},
		{
			name: "shared accepts custom loopback websocket",
			goos: "darwin",
			config: Config{
				CodexMode: "shared", CodexSock: "ws://localhost:48000/custom", CodexDesktopShare: true,
			},
			want: [][]string{
				{"unsetenv", codexDesktopLocalDaemonEnv},
				{"setenv", codexDesktopWebSocketEnv, "ws://localhost:48000/custom"},
			},
		},
		{
			name: "isolated clears both sharing mechanisms",
			goos: "darwin",
			config: Config{
				CodexMode: "isolated", CodexSock: "/Users/tester/.macfleet/codex-app-server.sock", CodexDesktopShare: true,
			},
			want: [][]string{
				{"unsetenv", codexDesktopLocalDaemonEnv},
				{"unsetenv", codexDesktopWebSocketEnv},
			},
		},
		{
			name:   "legacy daemon clears both sharing mechanisms",
			goos:   "darwin",
			config: Config{CodexMode: "daemon", CodexDesktopShare: true},
			want: [][]string{
				{"unsetenv", codexDesktopLocalDaemonEnv},
				{"unsetenv", codexDesktopWebSocketEnv},
			},
		},
		{
			name:   "explicitly disabled clears both sharing mechanisms",
			goos:   "darwin",
			config: Config{CodexMode: "shared", CodexDesktopShare: false},
			want: [][]string{
				{"unsetenv", codexDesktopLocalDaemonEnv},
				{"unsetenv", codexDesktopWebSocketEnv},
			},
		},
		{
			name: "shared rejects a mesh websocket endpoint",
			goos: "darwin",
			config: Config{
				CodexMode: "shared", CodexSock: "ws://100.64.0.2:47682/rpc", CodexDesktopShare: true,
			},
			wantError: "loopback",
		},
		{
			name:   "non darwin does nothing",
			goos:   "linux",
			config: Config{CodexMode: "shared", CodexDesktopShare: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operations, err := codexDesktopLaunchctlOperations(tt.config, tt.goos)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error got %v want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := launchctlArgs(operations); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("operations got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestConfigureCodexDesktopSharedDaemonRunsEveryOperation(t *testing.T) {
	previous := runCodexDesktopLaunchctl
	t.Cleanup(func() { runCodexDesktopLaunchctl = previous })

	var got [][]string
	runCodexDesktopLaunchctl = func(args ...string) ([]byte, error) {
		got = append(got, append([]string(nil), args...))
		return nil, nil
	}
	config := Config{CodexMode: "shared", CodexDesktopShare: true}
	if err := configureCodexDesktopSharedDaemon(config, "/Users/tester", "darwin"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"unsetenv", codexDesktopLocalDaemonEnv},
		{"setenv", codexDesktopWebSocketEnv, codexSharedWebSocketEndpoint},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launchctl operations got %#v want %#v", got, want)
	}
}

func TestConfigureCodexDesktopSharedDaemonReportsFailuresButContinuesCleanup(t *testing.T) {
	previous := runCodexDesktopLaunchctl
	t.Cleanup(func() { runCodexDesktopLaunchctl = previous })

	var got [][]string
	runCodexDesktopLaunchctl = func(args ...string) ([]byte, error) {
		got = append(got, append([]string(nil), args...))
		if args[1] == codexDesktopLocalDaemonEnv {
			return []byte("first mutation failed"), errors.New("exit 1")
		}
		return nil, nil
	}
	config := Config{CodexMode: "isolated"}
	err := configureCodexDesktopSharedDaemon(config, "/Users/tester", "darwin")
	if err == nil || !strings.Contains(err.Error(), "first mutation failed") {
		t.Fatalf("error got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("must attempt every cleanup operation, got %#v", got)
	}
}
