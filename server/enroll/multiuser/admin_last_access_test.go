package multiuser

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminLastAccessPersistsAfterLogoutAndRestart(t *testing.T) {
	server, now := newTestServer(t)
	member, user, _, _ := registerBrowser(t, server, *now, "last-access-member@example.com")
	admin, administrator, _, _ := registerBrowser(t, server, *now, "last-access-admin@example.com")
	if err := server.SetAdmin(administrator.Email); err != nil {
		t.Fatal(err)
	}
	check := func(want int64) {
		t.Helper()
		for _, path := range []string{fmt.Sprintf("/api/admin/users/%d", user.ID), "/api/admin/users?search=last-access-member"} {
			response := admin.request("GET", path, nil)
			var body struct {
				User  map[string]any   `json:"user"`
				Users []map[string]any `json:"users"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
				t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
			}
			if len(body.Users) == 1 {
				body.User = body.Users[0]
			}
			if body.User["last_access_at"] != float64(want) {
				t.Fatalf("%s: last_access_at=%v, want %d", path, body.User["last_access_at"], want)
			}
		}
	}
	check(now.Unix())
	lastBrowserAccess := now.Unix()
	addAppearanceDevice(t, server, user, 1, "active")
	token, _, err := server.issueDeviceCredentials(1)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	if response := deviceRequest(server, "GET", "/api/device/status", token); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	check(lastBrowserAccess)
	*now = now.Add(6 * time.Minute)
	if response := member.request("GET", "/api/auth/me", nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	check(now.Unix())
	*now = now.Add(time.Second)
	if response := member.request("POST", "/api/auth/logout", nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	lastAccess := now.Unix()
	check(lastAccess)
	*now = now.Add(time.Hour)
	check(lastAccess)
	options := server.options
	server.Close()
	reopened, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	admin.server = reopened
	check(lastAccess)
}

func TestLastAccessDoesNotMoveBackwards(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "monotonic-access@example.com")
	newer := now.Add(time.Minute).Unix()
	if _, err := server.db.Exec("UPDATE users SET last_access_at=? WHERE id=?", newer, user.ID); err != nil {
		t.Fatal(err)
	}
	if response := browser.request("GET", "/api/auth/me", nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var timestamp int64
	if err := server.db.QueryRow("SELECT last_access_at FROM users WHERE id=?", user.ID).Scan(&timestamp); err != nil || timestamp != newer {
		t.Fatalf("older request replaced newer activity: %d, error=%v", timestamp, err)
	}
}

func TestLastAccessMigrationKeepsHistoricalFullSessionActivity(t *testing.T) {
	server, now := newTestServer(t)
	_, user, _, _ := registerBrowser(t, server, *now, "legacy-access@example.com")
	initial := now.Unix()
	// Recreate the previous schema, regardless of whether the new column exists yet.
	rows, err := server.db.Query("PRAGMA table_info(users)")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for rows.Next() {
		var index, required, primary int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&index, &name, &kind, &required, &defaultValue, &primary); err != nil {
			t.Fatal(err)
		}
		found = found || name == "last_access_at"
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if found {
		if _, err := server.db.Exec("ALTER TABLE users DROP COLUMN last_access_at"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := server.db.Exec("UPDATE sessions SET last_seen=?,expires_at=? WHERE user_id=?", initial+20, initial+40, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := server.db.Exec("INSERT INTO sessions(token_hash,user_id,csrf,stage,created_at,expires_at,last_seen,agent,ip) VALUES('pending',?,'test','challenge',?,?,?,'test','127.0.0.1')", user.ID, initial, initial+3600, initial+50); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(100 * time.Second)
	options := server.options
	if _, err := server.db.Exec("CREATE TRIGGER reject_access_backfill BEFORE UPDATE ON users BEGIN SELECT RAISE(ABORT,'test backfill failure'); END"); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if reopened, err := New(options); err == nil {
		reopened.Close()
		t.Fatal("failed backfill must stop startup")
	}
	legacy, err := sql.Open("sqlite", filepath.Join(options.StateDir, "fleet.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := legacy.QueryRow("SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='last_access_at'").Scan(&columns); err != nil || columns != 0 {
		legacy.Close()
		t.Fatalf("failed backfill must roll back the new column: count=%d, error=%v", columns, err)
	}
	if _, err := legacy.Exec("DROP TRIGGER reject_access_backfill"); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	legacy.Close()
	for attempt := 0; attempt < 2; attempt++ {
		reopened, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		var timestamp int64
		err = reopened.db.QueryRow("SELECT last_access_at FROM users WHERE id=?", user.ID).Scan(&timestamp)
		reopened.Close()
		if err != nil || timestamp != initial+20 {
			t.Fatalf("migration %d: last_access_at=%d, want %d, error=%v", attempt, timestamp, initial+20, err)
		}
	}
}

func TestPendingAuthenticationDoesNotRecordLastAccess(t *testing.T) {
	server, now := newTestServer(t)
	browser := &testBrowser{server: server}
	response := browser.request("POST", "/api/auth/register", map[string]string{"email": "pending-access@example.com", "password": "correct horse battery", "confirm_password": "correct horse battery"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	*now = now.Add(time.Minute)
	if response = browser.request("GET", "/api/auth/qr", nil); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var timestamp int64
	if err := server.db.QueryRow("SELECT last_access_at FROM users WHERE email='pending-access@example.com'").Scan(&timestamp); err != nil || timestamp != 0 {
		t.Fatalf("pending last access=%d, error=%v", timestamp, err)
	}
}
