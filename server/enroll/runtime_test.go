package main

import (
	"strings"
	"testing"
)

func TestUnifiedConfigurationRejectsMissingOrigin(t *testing.T) {
	t.Setenv("FLEET_MESH_PROXY", "")
	t.Setenv("FLEET_ORIGIN", "")
	if _, err := serverOptionsFromEnv(); err == nil {
		t.Fatal("unified server must not silently use old global owner gateway")
	}
	t.Setenv("FLEET_ORIGIN", "http://127.0.0.1:7099")
	t.Setenv("FLEET_STATE_DIR", t.TempDir())
	options, err := serverOptionsFromEnv()
	if err != nil || options.Origin != "http://127.0.0.1:7099" {
		t.Fatalf("local options %+v %v", options, err)
	}
	t.Setenv("FLEET_HEADSCALE_URL", "https://headscale.example.com")
	t.Setenv("FLEET_HEADSCALE_API_KEY_FILE", "/nonexistent/macfleet-key")
	if _, err = serverOptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "Headscale") {
		t.Fatalf("missing headscale key %v", err)
	}
}

func TestUnifiedConfigurationUsesExplicitPrivateMeshProxy(t *testing.T) {
	t.Setenv("FLEET_ORIGIN", "http://127.0.0.1:7099")
	t.Setenv("FLEET_HEADSCALE_URL", "")
	t.Setenv("FLEET_MESH_PROXY", "http://127.0.0.1:17091")
	options, err := serverOptionsFromEnv()
	if err != nil || options.MeshProxy != "http://127.0.0.1:17091" {
		t.Fatalf("explicit mesh proxy: %v", err)
	}
	t.Setenv("FLEET_MESH_PROXY", "http://example.com:8080")
	if _, err := serverOptionsFromEnv(); err == nil {
		t.Fatal("accepted non-loopback mesh proxy")
	}
}
