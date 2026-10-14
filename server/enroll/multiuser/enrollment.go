package multiuser

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

func (server *Server) handleEnrollment(writer http.ResponseWriter, request *http.Request, user User) {
	if origin := request.Header.Get("Origin"); origin != "" && origin != server.options.Origin {
		reject(writer, 403, "请求来源不合法")
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/api/enrollment/")
	var input struct {
		Name  string `json:"name"`
		Code  string `json:"code"`
		ID    string `json:"request_id"`
		Token string `json:"claim_token"`
	}
	if !decode(writer, request, &input) {
		return
	}
	// Only requests for the same pairing serialize. A slow grant must not
	// block another user's start/confirmation or account settings.
	lockKey := input.ID
	if path == "start" {
		lockKey = ""
	}
	if path == "confirm" {
		_ = server.db.QueryRow("SELECT id FROM enrollments WHERE code=?", strings.ToUpper(strings.TrimSpace(input.Code))).Scan(&lockKey)
	}
	release := server.lockEnrollment(lockKey)
	defer release()
	if path == "start" {
		if !server.allowedAuth(request, "enrollment-start") {
			reject(writer, 429, "请求过于频繁")
			return
		}
		name := cleanName(input.Name)
		if name == "" {
			name = "Mac"
		}
		id, token := randomToken(), randomToken()
		code := strings.ToUpper(randomToken()[:8])
		expires := server.now().Add(10 * time.Minute).Unix()
		if _, err := server.db.Exec("INSERT INTO enrollments(id,code,claim_hash,name,state,expires_at) VALUES(?,?,?,?,'pending',?)", id, code, digest(token), name, expires); err != nil {
			server.failure(writer, err)
			return
		}
		respond(writer, 200, map[string]any{"request_id": id, "claim_token": token, "code": code, "verification_url": server.options.Origin + "/enroll/confirm?code=" + code, "expires_at": expires})
		return
	}
	if path == "confirm" {
		var id, name, state string
		var owner sql.NullInt64
		var expires int64
		err := server.db.QueryRow("SELECT id,name,state,user_id,expires_at FROM enrollments WHERE code=?", strings.ToUpper(strings.TrimSpace(input.Code))).Scan(&id, &name, &state, &owner, &expires)
		if err != nil || expires <= server.now().Unix() {
			reject(writer, 404, "绑定请求不存在或已过期")
			return
		}
		if owner.Valid {
			if owner.Int64 != user.ID {
				reject(writer, 409, "该请求已由其他账号确认")
				return
			}
			respond(writer, 200, map[string]bool{"ok": true})
			return
		}
		if user.Status != "active" || state != "pending" {
			reject(writer, 409, "绑定请求状态不合法")
			return
		}
		transaction, err := server.db.Begin()
		if err != nil {
			server.failure(writer, err)
			return
		}
		defer transaction.Rollback()
		result, err := transaction.Exec("INSERT INTO devices(user_id,name,status,created_at) VALUES(?,?,'installing',?)", user.ID, name, server.now().Unix())
		if err != nil {
			server.failure(writer, err)
			return
		}
		index, _ := result.LastInsertId()
		if _, err = transaction.Exec("UPDATE enrollments SET user_id=?,device_index=?,state='confirmed' WHERE id=? AND user_id IS NULL", user.ID, index, id); err != nil {
			server.failure(writer, err)
			return
		}
		if err = transaction.Commit(); err != nil {
			server.failure(writer, err)
			return
		}
		server.audit(user.ID, user.ID, "device_confirmed:m"+intString(index))
		respond(writer, 200, map[string]any{"ok": true, "device_id": "m" + intString(index), "name": name})
		return
	}
	var state, hash, grantID string
	var owner, index sql.NullInt64
	var expires int64
	var sealed []byte
	err := server.db.QueryRow("SELECT state,claim_hash,user_id,device_index,expires_at,grant_key,grant_id FROM enrollments WHERE id=?", input.ID).Scan(&state, &hash, &owner, &index, &expires, &sealed, &grantID)
	if err != nil || hash != digest(input.Token) {
		reject(writer, 404, "绑定请求不存在")
		return
	}
	if state == "complete" {
		if _, err = server.AuthorizedDevice(owner.Int64, "m"+intString(index.Int64)); err != nil {
			reject(writer, 410, "设备授权已撤销")
			return
		}
		respond(writer, 200, map[string]bool{"ok": true})
		return
	}
	if expires <= server.now().Unix() {
		reject(writer, 410, "绑定请求已过期，请重新安装")
		return
	}
	if !owner.Valid {
		respond(writer, 202, map[string]string{"state": "pending"})
		return
	}
	current, err := server.user(owner.Int64)
	if err != nil || current.Status != "active" {
		reject(writer, 403, "账号当前不能绑定设备")
		return
	}
	var deviceStatus, deviceName string
	if err = server.db.QueryRow("SELECT status,name FROM devices WHERE device_index=?", index.Int64).Scan(&deviceStatus, &deviceName); err != nil || deviceStatus == "revoked" {
		reject(writer, 410, "设备授权已撤销")
		return
	}
	if server.options.Network == nil {
		reject(writer, 503, "Headscale 未配置，无法签发入网凭据")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	if path == "claim" {
		if len(sealed) == 0 {
			grant, err := server.options.Network.Issue(ctx, owner.Int64, int(index.Int64))
			if err != nil {
				reject(writer, 503, "签发入网凭据失败，请重试")
				return
			}
			if !server.enrollmentStillValid(input.ID, owner.Int64, index.Int64) {
				reject(writer, 410, "设备授权已变更，请重新关联")
				return
			}
			sealed, err = server.seal(grant.Key)
			if err != nil {
				server.failure(writer, err)
				return
			}
			grantID = grant.KeyID
			if _, err = server.db.Exec("UPDATE enrollments SET grant_key=?,grant_id=?,state='issued' WHERE id=?", sealed, grantID, input.ID); err != nil {
				server.failure(writer, err)
				return
			}
		}
		key, err := server.unseal(sealed)
		if err != nil {
			server.failure(writer, err)
			return
		}
		token, proxy, err := server.issueDeviceCredentials(index.Int64)
		if err != nil {
			server.failure(writer, err)
			return
		}
		respond(writer, 200, map[string]any{"authKey": key, "loginServer": server.options.LoginServer, "index": intString(index.Int64), "state": "issued", "device_id": "m" + intString(index.Int64), "device_name": deviceName, "owner_email": current.Email, "device_token": token, "proxy_token": proxy, "agent_port": server.options.AgentPort, "terminal_port": server.options.TerminalPort, "files_port": server.options.FilesPort})
		return
	}
	if path != "complete" || grantID == "" {
		reject(writer, 409, "请先领取入网凭据")
		return
	}
	node, err := server.options.Network.Discover(ctx, Grant{KeyID: grantID})
	if err != nil {
		respond(writer, 202, map[string]string{"state": "installing"})
		return
	}
	if !server.enrollmentStillValid(input.ID, owner.Int64, index.Int64) {
		reject(writer, 410, "设备授权已变更，请重新关联")
		return
	}
	if _, err = server.db.Exec("UPDATE devices SET node_id=?,ip=?,online=?,last_seen=?,status='active' WHERE device_index=? AND user_id=? AND status!='revoked' AND EXISTS(SELECT 1 FROM users WHERE id=devices.user_id AND status='active')", node.ID, node.IP, node.Online, node.LastSeen, index.Int64, owner.Int64); err != nil {
		server.failure(writer, err)
		return
	}
	if err = server.syncNetwork(ctx); err != nil {
		server.db.Exec("UPDATE devices SET status='installing' WHERE device_index=? AND status='active'", index.Int64)
		reject(writer, 503, "网络策略尚未生效，请重试完成绑定")
		return
	}
	if !server.enrollmentStillValid(input.ID, owner.Int64, index.Int64) {
		reject(writer, 410, "设备授权已变更，请重新关联")
		return
	}
	transaction, err := server.db.Begin()
	if err != nil {
		server.failure(writer, err)
		return
	}
	defer transaction.Rollback()
	if _, err = transaction.Exec("UPDATE enrollments SET state='complete',grant_key=NULL WHERE id=?", input.ID); err != nil {
		server.failure(writer, err)
		return
	}
	if _, err = transaction.Exec("UPDATE device_credentials SET claim_token=NULL WHERE device_index=?", index.Int64); err != nil {
		server.failure(writer, err)
		return
	}
	if err = transaction.Commit(); err != nil {
		server.failure(writer, err)
		return
	}
	server.audit(owner.Int64, owner.Int64, "device_bound:m"+intString(index.Int64))
	respond(writer, 200, map[string]any{"ok": true, "device_id": fmt.Sprintf("m%d", index.Int64)})
}

// enrollmentStillValid prevents an in-flight grant/discovery from restoring a
// device revoked, expired or disabled while Headscale was responding.
func (server *Server) enrollmentStillValid(id string, owner, index int64) bool {
	var count int
	err := server.db.QueryRow(`SELECT COUNT(*) FROM enrollments e JOIN devices d ON d.device_index=e.device_index JOIN users u ON u.id=e.user_id WHERE e.id=? AND e.user_id=? AND e.device_index=? AND e.expires_at>? AND d.status!='revoked' AND u.status='active'`, id, owner, index, server.now().Unix()).Scan(&count)
	return err == nil && count == 1
}

func (server *Server) syncNetwork(ctx context.Context) (err error) {
	if server.options.Network == nil {
		return nil
	}
	// Serialize policy writes, not account traffic. If state changes during an
	// external write, reconcile a fresh snapshot before declaring it synchronized.
	server.networkMu.Lock()
	defer server.networkMu.Unlock()
	defer func() {
		if err != nil {
			server.networkSync.Store(0)
		} else {
			server.networkSync.Store(server.now().Unix())
		}
	}()
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		devices, e := server.Devices(0)
		if e != nil {
			return e
		}
		users, e := server.Users()
		if e != nil {
			return e
		}
		if err = server.options.Network.Reconcile(ctx, devices, users); err != nil {
			return err
		}
		latestDevices, e := server.Devices(0)
		if e != nil {
			return e
		}
		latestUsers, e := server.Users()
		if e != nil {
			return e
		}
		if reflect.DeepEqual(policySnapshot(devices, users), policySnapshot(latestDevices, latestUsers)) {
			return nil
		}
	}
}

func (server *Server) refreshNetwork(ctx context.Context) error {
	var revoked []string
	if snapshot, ok := server.options.Network.(interface {
		Nodes(context.Context) ([]Node, error)
	}); ok {
		nodes, err := snapshot.Nodes(ctx)
		if err != nil {
			return err
		}
		byID := make(map[string]Node, len(nodes))
		for _, node := range nodes {
			byID[node.ID] = node
		}
		server.mu.Lock()
		devices, err := server.Devices(0)
		if err != nil {
			server.mu.Unlock()
			return err
		}
		for _, device := range devices {
			if device.Status == "revoked" {
				if device.NodeID != "" {
					revoked = append(revoked, device.NodeID)
				}
				continue
			}
			if device.Status != "active" && device.Status != "unavailable" {
				continue
			}
			node, found := byID[device.NodeID]
			if !found {
				_, err = server.db.Exec("UPDATE devices SET online=0,status='unavailable' WHERE device_index=? AND status IN ('active','unavailable')", device.Index)
				if device.Status == "active" {
					server.cancelScopes(device.UserID, device.ID)
				}
			} else {
				if node.IP != device.IP {
					server.cancelScopes(device.UserID, device.ID)
				}
				_, err = server.db.Exec("UPDATE devices SET online=?,last_seen=?,ip=?,status='active' WHERE device_index=? AND status IN ('active','unavailable')", node.Online, node.LastSeen, node.IP, device.Index)
			}
			if err != nil {
				server.mu.Unlock()
				return err
			}
		}
		server.mu.Unlock()
	}
	if err := server.syncNetwork(ctx); err != nil {
		return err
	}
	for _, nodeID := range revoked {
		if err := server.options.Network.Revoke(ctx, nodeID); err != nil {
			return err
		}
	}
	return nil
}

func (server *Server) Run(ctx context.Context) error {
	defer server.networkSync.Store(0)
	if server.options.Network == nil {
		return nil
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := server.refreshNetwork(bounded)
		cancel()
		if err != nil {
			server.networkSync.Store(0)
			log.Printf("network reconciliation will retry: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-server.root.Done():
			return nil
		case <-ticker.C:
		}
	}
}

type enrollmentLock struct {
	mu         sync.Mutex
	references int
}

func (server *Server) lockEnrollment(key string) func() {
	if key == "" {
		return func() {}
	}
	server.enrollmentMu.Lock()
	if server.enrollmentLocks == nil {
		server.enrollmentLocks = map[string]*enrollmentLock{}
	}
	lock := server.enrollmentLocks[key]
	if lock == nil {
		lock = &enrollmentLock{}
		server.enrollmentLocks[key] = lock
	}
	lock.references++
	server.enrollmentMu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		server.enrollmentMu.Lock()
		lock.references--
		if lock.references == 0 {
			delete(server.enrollmentLocks, key)
		}
		server.enrollmentMu.Unlock()
	}
}

// Only access-policy inputs participate in revalidation. Login timestamps,
// display names and online telemetry must not keep a successful ACL write busy.
type policyDevice struct {
	Owner    int64
	Node, IP string
}
type accessPolicySnapshot struct {
	Users   map[int64]bool
	Devices map[int]policyDevice
}

func policySnapshot(devices []Device, users []User) accessPolicySnapshot {
	result := accessPolicySnapshot{Users: map[int64]bool{}, Devices: map[int]policyDevice{}}
	for _, user := range users {
		if user.Status == "active" {
			result.Users[user.ID] = true
		}
	}
	for _, device := range devices {
		if device.Status == "active" && result.Users[device.UserID] {
			result.Devices[device.Index] = policyDevice{device.UserID, device.NodeID, device.IP}
		}
	}
	return result
}
