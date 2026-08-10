package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestCodexDesktopSharedDaemonLaunchctlArgs(t *testing.T) {
	home := "/Users/tester"
	defaultHome := home + "/.codex"
	defaultSocket := defaultHome + "/app-server-control/app-server-control.sock"
	tests := []struct {
		name   string
		goos   string
		config Config
		want   []string
	}{
		{
			name: "auto default paths enables shared daemon",
			goos: "darwin",
			config: Config{
				CodexHome: defaultHome, CodexMode: "auto", CodexDesktopShare: true,
			},
			want: []string{"setenv", codexDesktopLocalDaemonEnv, "1"},
		},
		{
			name: "daemon explicit default socket enables shared daemon",
			goos: "darwin",
			config: Config{
				CodexHome: defaultHome, CodexMode: "daemon", CodexSock: defaultSocket, CodexDesktopShare: true,
			},
			want: []string{"setenv", codexDesktopLocalDaemonEnv, "1"},
		},
		{
			name: "stdio removes inherited shared daemon setting",
			goos: "darwin",
			config: Config{
				CodexHome: defaultHome, CodexMode: "stdio", CodexDesktopShare: true,
			},
			want: []string{"unsetenv", codexDesktopLocalDaemonEnv},
		},
		{
			name: "custom home cannot share desktop hard coded socket",
			goos: "darwin",
			config: Config{
				CodexHome: home + "/.codex-fleet", CodexMode: "auto", CodexDesktopShare: true,
			},
			want: []string{"unsetenv", codexDesktopLocalDaemonEnv},
		},
		{
			name: "custom socket cannot share desktop hard coded socket",
			goos: "darwin",
			config: Config{
				CodexHome: defaultHome, CodexMode: "auto", CodexSock: home + "/custom.sock", CodexDesktopShare: true,
			},
			want: []string{"unsetenv", codexDesktopLocalDaemonEnv},
		},
		{
			name: "explicitly disabled removes inherited setting",
			goos: "darwin",
			config: Config{
				CodexHome: defaultHome, CodexMode: "auto", CodexDesktopShare: false,
			},
			want: []string{"unsetenv", codexDesktopLocalDaemonEnv},
		},
		{
			name: "non darwin does nothing",
			goos: "linux",
			config: Config{
				CodexHome: defaultHome, CodexMode: "auto", CodexDesktopShare: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexDesktopSharedDaemonLaunchctlArgs(tt.config, home, tt.goos); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("args got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestConfigureCodexDesktopSharedDaemon(t *testing.T) {
	previous := runCodexDesktopLaunchctl
	t.Cleanup(func() { runCodexDesktopLaunchctl = previous })

	var got []string
	runCodexDesktopLaunchctl = func(args ...string) ([]byte, error) {
		got = append([]string(nil), args...)
		return nil, nil
	}
	config := Config{CodexHome: "/Users/tester/.codex", CodexMode: "auto", CodexDesktopShare: true}
	if err := configureCodexDesktopSharedDaemon(config, "/Users/tester", "darwin"); err != nil {
		t.Fatal(err)
	}
	want := []string{"setenv", codexDesktopLocalDaemonEnv, "1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launchctl args got %#v want %#v", got, want)
	}

	runCodexDesktopLaunchctl = func(...string) ([]byte, error) {
		return []byte("launchctl failed"), errors.New("exit 1")
	}
	if err := configureCodexDesktopSharedDaemon(config, "/Users/tester", "darwin"); err == nil {
		t.Fatal("expected launchctl error")
	}
}
