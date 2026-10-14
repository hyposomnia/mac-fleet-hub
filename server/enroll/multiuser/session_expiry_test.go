package multiuser

import (
	"context"
	"testing"
	"time"
)

func TestConnectionExpiryHonorsRenewedSessionWithoutRenewingIt(t *testing.T) {
	server, now := newTestServer(t)
	browser, _, _, _ := registerBrowser(t, server, *now, "connection-expiry@example.com")
	hash := digest(browser.cookie.Value)
	var expires int64
	if err := server.db.QueryRow("SELECT expires_at FROM sessions WHERE token_hash=?", hash).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.watchSessionExpiry(ctx, cancel, hash, now.Unix()-1)
	select {
	case <-ctx.Done():
		t.Fatal("renewed session was disconnected at its previous expiry")
	case <-time.After(30 * time.Millisecond):
	}
	var after int64
	server.db.QueryRow("SELECT expires_at FROM sessions WHERE token_hash=?", hash).Scan(&after)
	if after != expires {
		t.Fatal("connection watcher renewed the login")
	}
	revoked, stop := context.WithCancel(context.Background())
	defer stop()
	server.db.Exec("DELETE FROM sessions WHERE token_hash=?", hash)
	server.watchSessionExpiry(revoked, stop, hash, now.Unix()-1)
	select {
	case <-revoked.Done():
	case <-time.After(time.Second):
		t.Fatal("deleted session was not disconnected")
	}
}
