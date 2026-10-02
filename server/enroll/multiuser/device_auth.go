package multiuser

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

func (server *Server) issueDeviceCredentials(index int64) (string, string, error) {
	var sealedToken, sealedProxy []byte
	err := server.db.QueryRow("SELECT claim_token,proxy_token FROM device_credentials WHERE device_index=?", index).Scan(&sealedToken, &sealedProxy)
	if err == sql.ErrNoRows {
		token, proxy := randomToken(), randomToken()
		sealedToken, err = server.seal(token)
		if err != nil {
			return "", "", err
		}
		sealedProxy, err = server.seal(proxy)
		if err != nil {
			return "", "", err
		}
		_, err = server.db.Exec("INSERT INTO device_credentials(device_index,token_hash,claim_token,proxy_token) VALUES(?,?,?,?)", index, digest(token), sealedToken, sealedProxy)
		return token, proxy, err
	}
	if err != nil {
		return "", "", err
	}
	token, err := server.unseal(sealedToken)
	if err != nil {
		return "", "", err
	}
	proxy, err := server.unseal(sealedProxy)
	return token, proxy, err
}

func (server *Server) ProxyCredential(owner int64, id string) (string, error) {
	device, err := server.AuthorizedDevice(owner, id)
	if err != nil {
		return "", err
	}
	var sealed []byte
	if err = server.db.QueryRow("SELECT proxy_token FROM device_credentials WHERE device_index=?", device.Index).Scan(&sealed); err != nil {
		return "", err
	}
	return server.unseal(sealed)
}

func (server *Server) enrollmentPreview(writer http.ResponseWriter, request *http.Request) {
	if request.Method != "GET" {
		reject(writer, 405, "仅支持 GET")
		return
	}
	var name, state string
	var expires int64
	err := server.db.QueryRow("SELECT name,state,expires_at FROM enrollments WHERE code=?", strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("code")))).Scan(&name, &state, &expires)
	if err != nil || expires <= server.now().Unix() {
		reject(writer, 404, "绑定请求不存在或已过期")
		return
	}
	respond(writer, 200, map[string]any{"name": name, "state": state, "expires_at": expires})
}

func (server *Server) handleDeviceAuth(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/api/device/status" && request.URL.Path != "/api/device/binding" {
		reject(writer, 404, "接口不存在")
		return
	}
	if (request.URL.Path == "/api/device/status" && request.Method != "GET") || (request.URL.Path == "/api/device/binding" && request.Method != "DELETE") {
		reject(writer, 405, "不支持的方法")
		return
	}
	if origin := request.Header.Get("Origin"); origin != "" {
		reject(writer, 403, "仅允许设备访问")
		return
	}
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || len(header) < 40 {
		reject(writer, 401, "设备凭据无效")
		return
	}
	var index int
	err := server.db.QueryRow("SELECT device_index FROM device_credentials WHERE token_hash=?", digest(strings.TrimPrefix(header, "Bearer "))).Scan(&index)
	if err != nil {
		reject(writer, 401, "设备凭据无效")
		return
	}
	devices, err := server.Devices(0)
	if err != nil {
		server.failure(writer, err)
		return
	}
	for _, device := range devices {
		if device.Index != index {
			continue
		}
		if request.Method == "DELETE" {
			server.revokeDevice(writer, request, device.UserID, device)
			return
		}
		if device.Status == "revoked" {
			reject(writer, 410, "设备授权已撤销")
			return
		}
		user, err := server.user(device.UserID)
		if err != nil || user.Status != "active" {
			reject(writer, 403, "账号已禁用")
			return
		}
		status := 202
		lease := int64(0)
		if device.Status == "active" {
			status = 200
			lease = server.now().Add(45 * time.Second).Unix()
		}
		respond(writer, status, map[string]any{"device_id": device.ID, "owner_email": user.Email, "state": device.Status, "lease_until": lease, "idleSec": 1800})
		return
	}
	reject(writer, 401, "设备凭据无效")
}
