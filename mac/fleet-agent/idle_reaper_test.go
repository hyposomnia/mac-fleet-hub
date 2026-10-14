package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestIdleReaperPreservesLiveTasksAndNeverSetting(t *testing.T) {
	snapshot := "fleet-live 0 1\nfleet-dead 0 1\nfleet-attached 1 1\nfleet-unknown 0 1\nother 0 1\nfleet-malformed x 1\n"
	for _, seconds := range []int64{0, 30} {
		var ended, killed []string
		reapIdleSessions(snapshot, 100, seconds, func(name string) (string, error) {
			switch name {
			case "fleet-live":
				return "1\n0\n", nil
			case "fleet-dead":
				return "1\n1\n", nil
			default:
				return "", errors.New("unavailable")
			}
		}, func(name string) { ended = append(ended, name) }, func(name string) { killed = append(killed, name) })
		var want []string
		if seconds > 0 {
			want = []string{"fleet-dead"}
		}
		if !reflect.DeepEqual(killed, want) || !reflect.DeepEqual(ended, want) {
			t.Fatalf("idle=%d killed=%v ended=%v", seconds, killed, ended)
		}
	}
}

func TestDesktopRefreshWaitsForIdleInput(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{`"HIDIdleTime" = 100000000`, false}, {`"HIDIdleTime" = 3000000000`, true}, {`unavailable`, false}, {`"HIDIdleTime" = invalid`, false},
	} {
		if got := desktopInputIdle(tc.input); got != tc.want {
			t.Fatalf("idle(%q)=%v", tc.input, got)
		}
	}
}
