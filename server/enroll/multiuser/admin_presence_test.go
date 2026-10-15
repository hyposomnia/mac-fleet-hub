package multiuser

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// ADM-2/3: presence describes recent browser activity, independently of devices.
func TestAdminUserPresence(t *testing.T) {
	server, now := newTestServer(t)
	admin, administrator, _, _ := registerBrowser(t, server, *now, "presence-admin@example.com")
	if err := server.SetAdmin(administrator.Email); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, status, stage string
		age, expires        int64
		want                bool
	}{
		{"recent", "active", "full", 0, 3600, true},
		{"boundary", "active", "full", 300, 3600, true},
		{"idle", "active", "full", 301, 3600, false},
		{"expired", "active", "full", 0, 0, false},
		{"challenge", "active", "challenge", 0, 3600, false},
		{"setup", "setup", "full", 0, 3600, false},
		{"disabled", "disabled", "full", 0, 3600, false},
		{"logged-out", "active", "", 0, 3600, false},
	}
	wants := map[string]bool{}
	for _, tc := range cases {
		email := tc.name + "@example.com"
		result, err := server.db.Exec("INSERT INTO users(email,password,status,totp,created_at) VALUES(?, 'unused', ?, X'', ?)", email, tc.status, now.Unix())
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if tc.stage != "" {
			_, err = server.db.Exec("INSERT INTO sessions(token_hash,user_id,csrf,stage,created_at,expires_at,last_seen,agent,ip) VALUES(?,?,'test',?,?,?,?, 'test','127.0.0.1')", tc.name, id, tc.stage, now.Unix(), now.Unix()+tc.expires, now.Unix()-tc.age)
			if err != nil {
				t.Fatal(err)
			}
		}
		// All users own an online device, including users without recent browser activity.
		for _, status := range []string{"active", "revoked"} {
			if _, err = server.db.Exec("INSERT INTO devices(user_id,name,status,online,created_at) VALUES(?,'Mac',?,1,?)", id, status, now.Unix()); err != nil {
				t.Fatal(err)
			}
		}
		wants[email] = tc.want
		response := admin.request("GET", fmt.Sprintf("/api/admin/users/%d", id), nil)
		var detail struct {
			User map[string]any `json:"user"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &detail) != nil {
			t.Fatalf("%s: %d %s", tc.name, response.Code, response.Body.String())
		}
		if online, ok := detail.User["online"].(bool); !ok || online != tc.want {
			t.Errorf("%s detail online=%v, want %v", tc.name, detail.User["online"], tc.want)
		}
	}
	response := admin.request("GET", "/api/admin/users?search=example.com&page=1", nil)
	var listing struct {
		Users    []map[string]any `json:"users"`
		PageSize int              `json:"page_size"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &listing) != nil {
		t.Fatalf("listing: %d %s", response.Code, response.Body.String())
	}
	if listing.PageSize != 20 {
		t.Errorf("page_size=%d, want 20", listing.PageSize)
	}
	for _, user := range listing.Users {
		email := user["email"].(string)
		if want, exists := wants[email]; exists {
			if online, ok := user["online"].(bool); !ok || online != want {
				t.Errorf("%s list online=%v, want %v", email, user["online"], want)
			}
			if user["device_count"] != float64(1) {
				t.Errorf("%s device_count=%v, want 1", email, user["device_count"])
			}
			delete(wants, email)
		}
	}
	if len(wants) != 0 {
		t.Fatalf("users missing from list: %v", wants)
	}
}

func TestAdminPresenceFollowsBrowserActivityAndLogout(t *testing.T) {
	server, now := newTestServer(t)
	member, user, _, _ := registerBrowser(t, server, *now, "presence-member@example.com")
	admin, administrator, _, _ := registerBrowser(t, server, *now, "presence-admin@example.com")
	if err := server.SetAdmin(administrator.Email); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		response := admin.request("GET", fmt.Sprintf("/api/admin/users/%d", user.ID), nil)
		var body struct {
			User struct {
				Online      *bool `json:"online"`
				DeviceCount int   `json:"device_count"`
			} `json:"user"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.User.Online == nil || *body.User.Online != want || body.User.DeviceCount != 0 {
			t.Fatalf("want online=%t and zero devices: %d %s", want, response.Code, response.Body.String())
		}
	}
	check(true)
	*now = now.Add(301 * time.Second)
	check(false)
	if response := member.request("GET", "/api/auth/me", nil); response.Code != 200 {
		t.Fatalf("browser activity: %d %s", response.Code, response.Body.String())
	}
	check(true)
	// An old second session must not hide recent activity, or revive it after logout.
	if _, err := server.db.Exec("INSERT INTO sessions(token_hash,user_id,csrf,stage,created_at,expires_at,last_seen,agent,ip) VALUES('older-session',?,'test','full',?,?,?,'test','127.0.0.1')", user.ID, now.Unix()-301, now.Unix()+3600, now.Unix()-301); err != nil {
		t.Fatal(err)
	}
	check(true)
	if response := member.request("POST", "/api/auth/logout", nil); response.Code != 200 {
		t.Fatalf("logout: %d %s", response.Code, response.Body.String())
	}
	check(false)
}
