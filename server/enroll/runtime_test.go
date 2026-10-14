package main

import (
	"fmt"
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

func TestUnifiedAuthenticationPolicyConfiguration(t *testing.T) {
	t.Setenv("FLEET_ORIGIN", "http://127.0.0.1:7099")
	t.Setenv("FLEET_HEADSCALE_URL", "")
	t.Setenv("FLEET_SESSION_LIFETIME", "72h")
	t.Setenv("FLEET_SESSION_SLIDING", "true")
	t.Setenv("FLEET_AUTH_WINDOW", "5m")
	t.Setenv("FLEET_AUTH_ATTEMPTS", "12")
	t.Setenv("FLEET_AUTH_CONCURRENCY", "6")
	t.Setenv("FLEET_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")
	options, err := serverOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if options.SessionLifetime.String() != "72h0m0s" || !options.SessionSliding || options.AuthWindow.String() != "5m0s" || options.AuthAttempts != 12 || options.AuthConcurrency != 6 || len(options.TrustedProxies) != 2 {
		t.Fatalf("policy not applied: %+v", options)
	}
	for _, item := range []struct{ key, value string }{{"FLEET_SESSION_LIFETIME", "nonsense"}, {"FLEET_AUTH_WINDOW", "0s"}, {"FLEET_AUTH_ATTEMPTS", "-1"}, {"FLEET_AUTH_CONCURRENCY", "huge"}, {"FLEET_SESSION_SLIDING", "perhaps"}, {"FLEET_TRUSTED_PROXIES", "not-a-network"}} {
		t.Run(item.key, func(t *testing.T) {
			t.Setenv(item.key, item.value)
			if _, err := serverOptionsFromEnv(); err == nil {
				t.Fatalf("accepted invalid %s", item.key)
			}
		})
	}
}

func TestUnifiedDeviceOutageGraceConfiguration(t *testing.T) {
	t.Setenv("FLEET_ORIGIN", "http://127.0.0.1:7099")
	t.Setenv("FLEET_HEADSCALE_URL", "")
	for _, value := range []string{"0", "120", "600"} {
		t.Setenv("FLEET_DEVICE_OUTAGE_GRACE_SECONDS", value)
		options, err := serverOptionsFromEnv()
		if err != nil || options.DeviceOutageGrace == nil || fmt.Sprint(int(options.DeviceOutageGrace.Seconds())) != value {
			t.Fatalf("grace=%s options=%v err=%v", value, options.DeviceOutageGrace, err)
		}
	}
	for _, value := range []string{"-1", "601", "forever"} {
		t.Setenv("FLEET_DEVICE_OUTAGE_GRACE_SECONDS", value)
		if _, err := serverOptionsFromEnv(); err == nil {
			t.Fatalf("accepted grace=%s", value)
		}
	}
}
