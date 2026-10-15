package multiuser

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const adminPageSize = 20
const adminOnlineWindow = 5 * time.Minute

type adminUser struct {
	User
	Online       bool  `json:"online"`
	LastAccessAt int64 `json:"last_access_at"`
}

type adminActivity struct {
	Online       bool
	LastAccessAt int64
}

func (server *Server) adminUserActivity(userID int64) (map[int64]adminActivity, error) {
	now := server.now()
	rows, err := server.db.Query(`SELECT users.id,users.last_access_at,
EXISTS(SELECT 1 FROM sessions WHERE sessions.user_id=users.id AND users.status='active'
AND sessions.stage='full' AND sessions.expires_at>? AND sessions.last_seen>=?)
FROM users WHERE (?=0 OR users.id=?)`, now.Unix(), now.Add(-adminOnlineWindow).Unix(), userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activity := map[int64]adminActivity{}
	for rows.Next() {
		var id int64
		var summary adminActivity
		if err := rows.Scan(&id, &summary.LastAccessAt, &summary.Online); err != nil {
			return nil, err
		}
		activity[id] = summary
	}
	return activity, rows.Err()
}

type ownedDevice struct {
	ID        string `json:"id"`
	Index     int    `json:"index"`
	IP        string `json:"ip"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Version   string `json:"version"`
	Online    bool   `json:"online"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
}

func orderOwnedDevices(devices []Device, order []string) []Device {
	byID := make(map[string]Device, len(devices))
	for _, device := range devices {
		byID[device.ID] = device
	}
	ordered := make([]Device, 0, len(devices))
	for _, id := range order {
		if device, exists := byID[id]; exists {
			ordered = append(ordered, device)
			delete(byID, id)
		}
	}
	for _, device := range devices {
		if _, exists := byID[device.ID]; exists {
			ordered = append(ordered, device)
		}
	}
	return ordered
}

func cleanName(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, strings.TrimSpace(value))
	characters := []rune(value)
	if len(characters) > 40 {
		value = string(characters[:40])
	}
	return value
}

func (server *Server) handleDevices(writer http.ResponseWriter, request *http.Request, user User) {
	path := request.URL.Path
	if strings.HasPrefix(path, "/api/devices/") {
		if request.Method != "DELETE" {
			reject(writer, 405, "仅支持 DELETE")
			return
		}
		id := strings.TrimPrefix(path, "/api/devices/")
		devices, err := server.Devices(user.ID)
		if err != nil {
			server.failure(writer, err)
			return
		}
		for _, device := range devices {
			if device.ID == id && device.Status != "revoked" {
				server.revokeDevice(writer, request, user.ID, device)
				return
			}
		}
		reject(writer, 404, "设备不存在")
		return
	}
	if path == "/api/settings" {
		server.handlePreferences(writer, request, user)
		return
	}
	devices, err := server.Devices(user.ID)
	if err != nil {
		server.failure(writer, err)
		return
	}
	if path == "/api/names" {
		if request.Method == "POST" {
			var input struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if !decode(writer, request, &input) {
				return
			}
			found := false
			for index, device := range devices {
				if device.ID == input.ID && device.Status != "revoked" {
					name := cleanName(input.Name)
					if name == "" {
						name = fmt.Sprintf("Mac %d", device.Index)
					}
					if _, err = server.db.Exec("UPDATE devices SET name=? WHERE device_index=? AND user_id=?", name, device.Index, user.ID); err != nil {
						server.failure(writer, err)
						return
					}
					devices[index].Name = name
					found = true
					break
				}
			}
			if !found {
				reject(writer, 404, "设备不存在")
				return
			}
		} else if request.Method != "GET" {
			reject(writer, 405, "不支持的方法")
			return
		}
		names := map[string]string{}
		for _, device := range devices {
			if device.Status != "revoked" {
				names[device.ID] = device.Name
			}
		}
		respond(writer, 200, names)
		return
	}
	if request.Method != "GET" {
		reject(writer, 405, "仅支持 GET")
		return
	}
	if path == "/api/nodes.json" {
		nodes := []map[string]any{}
		for _, device := range devices {
			if device.Status == "active" {
				nodes = append(nodes, map[string]any{"givenName": fmt.Sprintf("mac%d", device.Index), "online": device.Online, "lastSeen": device.LastSeen})
			}
		}
		respond(writer, 200, nodes)
		return
	}
	visible := []Device{}
	for _, device := range devices {
		if device.Status != "revoked" {
			visible = append(visible, device)
		}
	}
	server.mu.Lock()
	preferences, preferenceErr := server.loadDashboardPreferences(user.ID)
	server.mu.Unlock()
	if preferenceErr != nil {
		server.failure(writer, preferenceErr)
		return
	}
	visible = orderOwnedDevices(visible, preferences.DeviceOrder)
	result := make([]ownedDevice, 0, len(visible))
	for _, device := range visible {
		result = append(result, ownedDevice{ID: device.ID, Index: device.Index, IP: device.IP, Name: device.Name,
			Status: device.Status, Version: device.Version, Online: device.Online, CreatedAt: device.CreatedAt, LastSeen: device.LastSeen})
	}
	respond(writer, 200, map[string]any{"devices": result})
}

func (server *Server) revokeDevice(writer http.ResponseWriter, request *http.Request, actor int64, device Device) {
	server.mu.Lock()
	if _, err := server.db.Exec("UPDATE devices SET status='revoked',online=0 WHERE device_index=?", device.Index); err != nil {
		server.mu.Unlock()
		server.failure(writer, err)
		return
	}
	server.cancelScopes(device.UserID, device.ID)
	server.audit(actor, device.UserID, "device_revoked:"+device.ID)
	server.mu.Unlock()
	if server.options.Network != nil {
		if err := server.syncNetwork(request.Context()); err != nil {
			respond(writer, 503, map[string]any{"error": "设备访问已撤销，网络策略等待重试", "access_revoked": true})
			return
		}
		if device.NodeID != "" {
			if err := server.options.Network.Revoke(request.Context(), device.NodeID); err != nil {
				respond(writer, 503, map[string]any{"error": "设备访问已撤销，网络撤销等待重试", "access_revoked": true})
				return
			}
		}
	}
	respond(writer, 200, map[string]bool{"ok": true})
}

func pageRequest(request *http.Request) int {
	page, _ := strconv.Atoi(request.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	return page
}
func pageBounds(page, total int) (int, int) {
	start := (page - 1) * adminPageSize
	if start > total || start < 0 {
		start = total
	}
	end := start + adminPageSize
	if end > total {
		end = total
	}
	return start, end
}

func (server *Server) handleAdmin(writer http.ResponseWriter, request *http.Request, actor User) {
	path := strings.TrimPrefix(request.URL.Path, "/api/admin/")
	if path == "overview" && request.Method == "GET" {
		users, err := server.Users()
		if err != nil {
			server.failure(writer, err)
			return
		}
		devices, err := server.Devices(0)
		if err != nil {
			server.failure(writer, err)
			return
		}
		count, online := 0, 0
		for _, device := range devices {
			if device.Status != "revoked" {
				count++
				if device.Online {
					online++
				}
			}
		}
		respond(writer, 200, map[string]int{"users": len(users), "devices": count, "online": online})
		return
	}
	search := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("search")))
	page := pageRequest(request)
	if path == "users" && request.Method == "GET" {
		users, err := server.Users()
		if err != nil {
			server.failure(writer, err)
			return
		}
		activity, err := server.adminUserActivity(0)
		if err != nil {
			server.failure(writer, err)
			return
		}
		filtered := []adminUser{}
		for _, user := range users {
			if strings.Contains(user.Email, search) {
				filtered = append(filtered, adminUser{User: user, Online: activity[user.ID].Online, LastAccessAt: activity[user.ID].LastAccessAt})
			}
		}
		start, end := pageBounds(page, len(filtered))
		respond(writer, 200, map[string]any{"users": filtered[start:end], "total": len(filtered), "page": page, "page_size": adminPageSize})
		return
	}
	if path == "devices" && request.Method == "GET" {
		devices, err := server.Devices(0)
		if err != nil {
			server.failure(writer, err)
			return
		}
		filtered := []Device{}
		for _, device := range devices {
			if strings.Contains(strings.ToLower(device.Name+" "+device.Email+" "+device.ID), search) {
				filtered = append(filtered, device)
			}
		}
		start, end := pageBounds(page, len(filtered))
		respond(writer, 200, map[string]any{"devices": filtered[start:end], "total": len(filtered), "page": page})
		return
	}
	if strings.HasPrefix(path, "devices/") && request.Method == "DELETE" {
		devices, err := server.Devices(0)
		if err != nil {
			server.failure(writer, err)
			return
		}
		for _, device := range devices {
			if device.ID == strings.TrimPrefix(path, "devices/") {
				server.revokeDevice(writer, request, actor.ID, device)
				return
			}
		}
		reject(writer, 404, "设备不存在")
		return
	}
	if !strings.HasPrefix(path, "users/") {
		reject(writer, 404, "接口不存在")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "users/"), "/")
	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		reject(writer, 404, "用户不存在")
		return
	}
	user, err := server.user(userID)
	if err != nil {
		reject(writer, 404, "用户不存在")
		return
	}
	if len(parts) == 2 && parts[1] == "revoke-sessions" && request.Method == "POST" {
		if err = server.revokeUserSessions(userID); err != nil {
			server.failure(writer, err)
			return
		}
		server.audit(actor.ID, userID, "sessions_revoked")
		respond(writer, 200, map[string]bool{"ok": true})
		return
	}
	if len(parts) != 1 {
		reject(writer, 404, "接口不存在")
		return
	}
	if request.Method == "GET" {
		activity, err := server.adminUserActivity(userID)
		if err != nil {
			server.failure(writer, err)
			return
		}
		devices, err := server.Devices(userID)
		if err != nil {
			server.failure(writer, err)
			return
		}
		eventPage, _ := strconv.Atoi(request.URL.Query().Get("event_page"))
		if eventPage < 1 {
			eventPage = 1
		}
		eventSearch := "%" + request.URL.Query().Get("event_search") + "%"
		var eventTotal int
		if err = server.db.QueryRow("SELECT COUNT(*) FROM audit_events WHERE target_id=? AND action LIKE ?", userID, eventSearch).Scan(&eventTotal); err != nil {
			server.failure(writer, err)
			return
		}
		rows, err := server.db.Query("SELECT actor_id,action,created_at FROM audit_events WHERE target_id=? AND action LIKE ? ORDER BY id DESC LIMIT 50 OFFSET ?", userID, eventSearch, (eventPage-1)*50)
		if err != nil {
			server.failure(writer, err)
			return
		}
		events := []map[string]any{}
		for rows.Next() {
			var actorID, created int64
			var action string
			rows.Scan(&actorID, &action, &created)
			events = append(events, map[string]any{"actor_id": actorID, "action": action, "created_at": created})
		}
		rows.Close()
		respond(writer, 200, map[string]any{"user": adminUser{User: user, Online: activity[user.ID].Online, LastAccessAt: activity[user.ID].LastAccessAt}, "devices": devices, "events": events, "event_page": eventPage, "event_total": eventTotal, "event_page_size": 50})
		return
	}
	if request.Method != "PATCH" {
		reject(writer, 405, "不支持的方法")
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if !decode(writer, request, &input) {
		return
	}
	if input.Status != "active" && input.Status != "disabled" {
		reject(writer, 400, "账号状态不合法")
		return
	}
	if userID == actor.ID {
		reject(writer, 400, "不能禁用或重置自己的账号状态")
		return
	}
	if input.Status == "active" && !user.TOTPBound {
		reject(writer, 400, "账号尚未完成验证器绑定")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if _, err = server.db.Exec("UPDATE users SET status=? WHERE id=?", input.Status, userID); err != nil {
		server.failure(writer, err)
		return
	}
	if err = server.revokeUserSessions(userID); err != nil {
		server.failure(writer, err)
		return
	}
	server.audit(actor.ID, userID, "account_"+input.Status)
	if server.options.Network != nil {
		server.mu.Unlock()
		err = server.syncNetwork(request.Context())
		server.mu.Lock()
		if err != nil {
			reject(writer, 503, "账号状态已更新，网络策略等待重试")
			return
		}
	}
	user, _ = server.user(userID)
	respond(writer, 200, map[string]any{"user": user})
}
