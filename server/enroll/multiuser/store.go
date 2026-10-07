package multiuser

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite"
)

type User struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	LastLogin   int64  `json:"last_login"`
	DeviceCount int    `json:"device_count"`
	TOTPBound   bool   `json:"totp_bound"`
}

type Device struct {
	ID        string `json:"id"`
	UserID    int64  `json:"user_id"`
	Index     int    `json:"index"`
	NodeID    string `json:"-"`
	IP        string `json:"-"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	Online    bool   `json:"online"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	Email     string `json:"email,omitempty"`
}

type Grant struct{ Key, KeyID string }
type Node struct {
	ID, IP, Name, KeyID string
	Online              bool
	LastSeen            int64
}
type Network interface {
	Issue(context.Context, int64, int) (Grant, error)
	Discover(context.Context, Grant) (Node, error)
	Reconcile(context.Context, []Device, []User) error
	Revoke(context.Context, string) error
}

const userColumns = "id,email,role,status,created_at,last_login,totp_confirmed,(SELECT COUNT(*) FROM devices WHERE devices.user_id=users.id AND status!='revoked')"

func scanUser(scanner interface{ Scan(...any) error }) (User, error) {
	var user User
	err := scanner.Scan(&user.ID, &user.Email, &user.Role, &user.Status, &user.CreatedAt, &user.LastLogin, &user.TOTPBound, &user.DeviceCount)
	return user, err
}

func (server *Server) openStore() error {
	if err := os.MkdirAll(server.options.StateDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(server.options.StateDir, "fleet.sqlite")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	file.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	server.db, err = sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	server.db.SetMaxOpenConns(1)
	_, err = server.db.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO schema_version VALUES(1);
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY AUTOINCREMENT,email TEXT NOT NULL UNIQUE,password TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'user',status TEXT NOT NULL DEFAULT 'setup',totp BLOB NOT NULL,totp_confirmed INTEGER NOT NULL DEFAULT 0,totp_step INTEGER NOT NULL DEFAULT -1,created_at INTEGER NOT NULL,last_login INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS sessions(token_hash TEXT PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id),csrf TEXT NOT NULL,stage TEXT NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,last_seen INTEGER NOT NULL,agent TEXT NOT NULL,ip TEXT NOT NULL,pending_secret BLOB);
CREATE TABLE IF NOT EXISTS recovery_codes(user_id INTEGER NOT NULL REFERENCES users(id),hash TEXT NOT NULL,PRIMARY KEY(user_id,hash));
CREATE TABLE IF NOT EXISTS devices(device_index INTEGER PRIMARY KEY AUTOINCREMENT,user_id INTEGER NOT NULL REFERENCES users(id),node_id TEXT NOT NULL DEFAULT '',ip TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,status TEXT NOT NULL,version TEXT NOT NULL DEFAULT '',online INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,last_seen INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS devices_node ON devices(node_id) WHERE node_id!='' AND status!='revoked';
CREATE TABLE IF NOT EXISTS device_credentials(device_index INTEGER PRIMARY KEY REFERENCES devices(device_index),token_hash TEXT NOT NULL UNIQUE,claim_token BLOB,proxy_token BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS enrollments(id TEXT PRIMARY KEY,code TEXT NOT NULL UNIQUE,claim_hash TEXT NOT NULL,user_id INTEGER REFERENCES users(id),device_index INTEGER REFERENCES devices(device_index),name TEXT NOT NULL,state TEXT NOT NULL,expires_at INTEGER NOT NULL,grant_key BLOB,grant_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS oauth_requests(id TEXT PRIMARY KEY,challenge TEXT NOT NULL UNIQUE,redirect_uri TEXT NOT NULL,state TEXT NOT NULL,name TEXT NOT NULL,status TEXT NOT NULL,expires_at INTEGER NOT NULL,code_hash TEXT UNIQUE,user_id INTEGER REFERENCES users(id));
CREATE TABLE IF NOT EXISTS preferences(user_id INTEGER PRIMARY KEY REFERENCES users(id),value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS audit_events(id INTEGER PRIMARY KEY AUTOINCREMENT,actor_id INTEGER NOT NULL,target_id INTEGER NOT NULL,action TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS migration_markers(name TEXT PRIMARY KEY,created_at INTEGER NOT NULL);`)
	return err
}

func (server *Server) Users() ([]User, error) {
	rows, err := server.db.Query("SELECT " + userColumns + " FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (server *Server) user(id int64) (User, error) {
	return scanUser(server.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id=?", id))
}

func (server *Server) Devices(userID int64) ([]Device, error) {
	query := "SELECT device_index,user_id,node_id,ip,name,devices.status,version,online,devices.created_at,last_seen,users.email FROM devices JOIN users ON users.id=devices.user_id"
	args := []any{}
	if userID > 0 {
		query += " WHERE user_id=?"
		args = append(args, userID)
	}
	query += " ORDER BY device_index"
	rows, err := server.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := []Device{}
	for rows.Next() {
		var device Device
		if err = rows.Scan(&device.Index, &device.UserID, &device.NodeID, &device.IP, &device.Name, &device.Status, &device.Version, &device.Online, &device.CreatedAt, &device.LastSeen, &device.Email); err != nil {
			return nil, err
		}
		device.ID = "m" + strconv.Itoa(device.Index)
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (server *Server) AuthorizedDevice(userID int64, id string) (Device, error) {
	user, err := server.user(userID)
	if err != nil || user.Status != "active" {
		return Device{}, sql.ErrNoRows
	}
	devices, err := server.Devices(userID)
	if err != nil {
		return Device{}, err
	}
	for _, device := range devices {
		if device.ID == id && device.Status == "active" && device.NodeID != "" && device.IP != "" {
			return device, nil
		}
	}
	return Device{}, sql.ErrNoRows
}

func (server *Server) SetAdmin(email string) error {
	result, err := server.db.Exec("UPDATE users SET role='admin' WHERE email=?", normalizeEmail(email))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errors.New("账号不存在")
	}
	return nil
}

func (server *Server) audit(actor, target int64, action string) error {
	_, err := server.db.Exec("INSERT INTO audit_events(actor_id,target_id,action,created_at) VALUES(?,?,?,?)", actor, target, action, server.now().Unix())
	return err
}
func intString(value int64) string { return strconv.FormatInt(value, 10) }
func randomToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (server *Server) seal(value string) ([]byte, error) {
	block, err := aes.NewCipher(server.options.Key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, []byte(value), []byte("mac-fleet-hub/totp/v1")), nil
}
func (server *Server) unseal(value []byte) (string, error) {
	block, err := aes.NewCipher(server.options.Key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(value) < aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted state")
	}
	plain, err := aead.Open(nil, value[:aead.NonceSize()], value[aead.NonceSize():], []byte("mac-fleet-hub/totp/v1"))
	return string(plain), err
}
