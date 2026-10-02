package multiuser

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"time"
)

func (server *Server) ImportLegacy(email string, devices []Device, preferences json.RawMessage) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	var ownerID int64
	if err := server.db.QueryRow("SELECT id FROM users WHERE email=?", normalizeEmail(email)).Scan(&ownerID); err != nil {
		return errors.New("迁移必须指定已注册账号")
	}
	var migrated int64
	err := server.db.QueryRow("SELECT created_at FROM migration_markers WHERE name='legacy-import'").Scan(&migrated)
	if err == nil {
		return nil
	}
	if len(preferences) > 0 && !json.Valid(preferences) {
		return errors.New("旧偏好格式不合法")
	}
	transaction, err := server.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, device := range devices {
		address, err := netip.ParseAddr(device.IP)
		if err != nil || !netip.MustParsePrefix("100.64.0.0/10").Contains(address) || device.NodeID == "" || device.Index <= 0 {
			return errors.New("旧设备必须具备已验证的 mesh IP、node ID 和编号")
		}
		if _, err = transaction.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,online,created_at,last_seen) VALUES(?,?,?,?,?,'active',?,?,?)", device.Index, ownerID, device.NodeID, device.IP, cleanName(device.Name), device.Online, server.now().Unix(), device.LastSeen); err != nil {
			return err
		}
	}
	if len(preferences) > 0 {
		if _, err = transaction.Exec("INSERT INTO preferences(user_id,value) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET value=excluded.value", ownerID, string(preferences)); err != nil {
			return err
		}
	}
	if _, err = transaction.Exec("INSERT INTO migration_markers(name,created_at) VALUES('legacy-import',?)", server.now().Unix()); err != nil {
		return err
	}
	return transaction.Commit()
}

func (server *Server) ResetPassword(email, password string) error {
	if !validPassword(password) {
		return errors.New("密码至少 12 字符，最多 256 字节")
	}
	encoded := hashPassword(password)
	server.mu.Lock()
	defer server.mu.Unlock()
	var id int64
	if err := server.db.QueryRow("SELECT id FROM users WHERE email=?", normalizeEmail(email)).Scan(&id); err != nil {
		return errors.New("账号不存在")
	}
	_, sealed, err := server.newTOTP(normalizeEmail(email))
	if err != nil {
		return err
	}
	transaction, err := server.db.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.Exec("UPDATE users SET password=?,status='setup',totp=?,totp_confirmed=0,totp_step=-1 WHERE id=?", encoded, sealed, id); err != nil {
		return err
	}
	if _, err = transaction.Exec("DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	if _, err = transaction.Exec("DELETE FROM recovery_codes WHERE user_id=?", id); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	server.cancelScopes(id, "")
	server.audit(id, id, "local_account_reset")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.syncNetwork(ctx)
}
