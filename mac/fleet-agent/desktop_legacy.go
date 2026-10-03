package main

import (
	"path/filepath"
	"strings"
)

func desktopLegacyCommandAllowed(executable string, action svcAction) bool {
	if !strings.HasSuffix(filepath.Clean(executable), "/Fleet Agent.app/Contents/MacOS/fleet-agent") {
		return true
	}
	switch action {
	case actStart, actStop, actRestart, actUpdate, actLogin, actLogout, actDoctor:
		return false
	default:
		return true
	}
}
