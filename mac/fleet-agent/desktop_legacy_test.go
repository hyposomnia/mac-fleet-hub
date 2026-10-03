package main

import "testing"

func TestDesktopBundleRejectsLegacyMutations(t *testing.T) {
	bundled := "/Applications/Fleet Hub.app/Contents/Library/LoginItems/Fleet Agent.app/Contents/MacOS/fleet-agent"
	for _, action := range []svcAction{actStart, actStop, actRestart, actUpdate, actLogin, actLogout, actDoctor} {
		if desktopLegacyCommandAllowed(bundled, action) {
			t.Fatalf("allowed legacy mutation %v", action)
		}
		if !desktopLegacyCommandAllowed("/Users/fixture/.local/bin/fleet-agent", action) {
			t.Fatal("broke standalone CLI")
		}
	}
	for _, action := range []svcAction{actVersion, actHelp, actCapabilities} {
		if !desktopLegacyCommandAllowed(bundled, action) {
			t.Fatal("blocked read-only command")
		}
	}
}
