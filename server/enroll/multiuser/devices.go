package multiuser

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

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
	respond(writer, 200, map[string]any{"devices": visible})
}

func (server *Server) handlePreferences(writer http.ResponseWriter, request *http.Request, user User) {
	defaults := map[string]int{"desktopMaxWindows": 10, "desktopScrollback": 5000, "mobileMaxWindows": 4, "mobileScrollback": 5000, "autoCloseMinutes": 30, "chatCacheMaxSessions": 6}
	if request.Method == "GET" {
		var raw string
		err := server.db.QueryRow("SELECT value FROM preferences WHERE user_id=?", user.ID).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			server.failure(writer, err)
			return
		}
		if raw != "" {
			json.Unmarshal([]byte(raw), &defaults)
		}
		respond(writer, 200, defaults)
		return
	}
	if request.Method != "POST" {
		reject(writer, 405, "不支持的方法")
		return
	}
	var input map[string]int
	if !decode(writer, request, &input) {
		return
	}
	limits := map[string][2]int{"desktopMaxWindows": {1, 30}, "desktopScrollback": {200, 100000}, "mobileMaxWindows": {1, 12}, "mobileScrollback": {200, 100000}, "autoCloseMinutes": {1, 1440}, "chatCacheMaxSessions": {1, 20}}
	for key, bounds := range limits {
		if value := input[key]; value != 0 {
			if value < bounds[0] {
				value = bounds[0]
			}
			if value > bounds[1] {
				value = bounds[1]
			}
			defaults[key] = value
		}
	}
	raw, _ := json.Marshal(defaults)
	if _, err := server.db.Exec("INSERT INTO preferences(user_id,value) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET value=excluded.value", user.ID, string(raw)); err != nil {
		server.failure(writer, err)
		return
	}
	respond(writer, 200, defaults)
}

func (server *Server) revokeDevice(writer http.ResponseWriter, request *http.Request, actor int64, device Device) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if _, err := server.db.Exec("UPDATE devices SET status='revoked',online=0 WHERE device_index=?", device.Index); err != nil {
		server.failure(writer, err)
		return
	}
	server.cancelScopes(device.UserID, device.ID)
	server.audit(actor, device.UserID, "device_revoked:"+device.ID)
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
	start := (page - 1) * 20
	if start > total || start < 0 {
		start = total
	}
	end := start + 20
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
		filtered := []User{}
		for _, user := range users {
			if strings.Contains(user.Email, search) {
				filtered = append(filtered, user)
			}
		}
		start, end := pageBounds(page, len(filtered))
		respond(writer, 200, map[string]any{"users": filtered[start:end], "total": len(filtered), "page": page})
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
		devices, err := server.Devices(userID)
		if err != nil {
			server.failure(writer, err)
			return
		}
		rows, err := server.db.Query("SELECT actor_id,action,created_at FROM audit_events WHERE target_id=? ORDER BY id DESC LIMIT 50", userID)
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
		respond(writer, 200, map[string]any{"user": user, "devices": devices, "events": events})
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
		if err = server.syncNetwork(request.Context()); err != nil {
			reject(writer, 503, "账号状态已更新，网络策略等待重试")
			return
		}
	}
	user, _ = server.user(userID)
	respond(writer, 200, map[string]any{"user": user})
}
