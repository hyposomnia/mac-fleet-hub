package multiuser

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// This capability can only read /api/standby. It is bound to the issuing full
// login and checked against the database on every request, including after I/O.
// It cannot be used as a browser cookie, device token, or automation API key.
type standbyGrant struct {
	Session string `json:"session"`
	Expires int64  `json:"expires"`
}

func (s *Server) standbyMAC(payload string) []byte {
	m := hmac.New(sha256.New, s.options.Key)
	m.Write([]byte("fleet-standby-v1\x00" + s.options.Origin + "\x00" + payload))
	return m.Sum(nil)
}
func (s *Server) mintStandbyToken(w http.ResponseWriter, r *http.Request, session sessionState) {
	if r.Method != http.MethodPost {
		reject(w, 405, "仅支持 POST")
		return
	}
	expires := min(session.Expires, s.now().Add(30*24*time.Hour).Unix())
	data, _ := json.Marshal(standbyGrant{session.Hash, expires})
	payload := base64.RawURLEncoding.EncodeToString(data)
	token := payload + "." + base64.RawURLEncoding.EncodeToString(s.standbyMAC(payload))
	respond(w, 200, map[string]any{"token": token, "expiresAt": expires * 1000})
}
func (s *Server) standbyUser(token string) (User, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 2048 {
		return User{}, errors.New("invalid capability")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, s.standbyMAC(parts[0])) {
		return User{}, errors.New("invalid capability")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return User{}, err
	}
	var grant standbyGrant
	if json.Unmarshal(data, &grant) != nil || grant.Expires <= s.now().Unix() {
		return User{}, errors.New("expired capability")
	}
	var userID, expires int64
	var stage string
	err = s.db.QueryRow("SELECT user_id,stage,expires_at FROM sessions WHERE token_hash=?", grant.Session).Scan(&userID, &stage, &expires)
	if err != nil || stage != "full" || expires <= s.now().Unix() {
		return User{}, errors.New("revoked capability")
	}
	user, err := s.user(userID)
	if err != nil || user.Status != "active" {
		return User{}, errors.New("inactive account")
	}
	return user, nil
}

type standbyModel struct {
	Assistant         string `json:"assistant"`
	Model             string `json:"model"`
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	CachedInputTokens int64  `json:"cachedInputTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens"`
	TotalTokens       int64  `json:"totalTokens"`
}
type standbyUsage struct {
	Date     string         `json:"date"`
	Timezone string         `json:"timezone"`
	Models   []standbyModel `json:"models"`
	Partial  bool           `json:"partial"`
	Warnings []string       `json:"warnings"`
}
type standbySession struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	SessionID  string `json:"sessionId"`
	Assistant  string `json:"assistant"`
	Title      string `json:"title"`
	Project    string `json:"project"`
	Running    bool   `json:"running"`
	Waiting    bool   `json:"waiting"`
	Unread     bool   `json:"unread"`
	ActivityAt int64  `json:"activityAt"`
}
type standbyHost struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	UsageStatus     string           `json:"usageStatus"`
	SessionsPartial bool             `json:"sessionsPartial"`
	Models          []standbyModel   `json:"-"`
	Sessions        []standbySession `json:"-"`
	NodeID          string           `json:"-"`
	IP              string           `json:"-"`
}
type standbySnapshot struct {
	Version         int              `json:"version"`
	Date            string           `json:"date"`
	Timezone        string           `json:"timezone"`
	GeneratedAt     int64            `json:"generatedAt"`
	Models          []standbyModel   `json:"models"`
	TotalTokens     int64            `json:"totalTokens"`
	Hosts           []standbyHost    `json:"hosts"`
	ReportingHosts  int              `json:"reportingHosts"`
	UsageComplete   bool             `json:"usageComplete"`
	Sessions        []standbySession `json:"sessions"`
	SessionsPartial bool             `json:"sessionsPartial"`
}

func (s *Server) standbyDeviceGET(ctx context.Context, device Device, route string) (*http.Response, error) {
	current, err := s.AuthorizedDevice(device.UserID, device.ID)
	if err != nil || current.IP != device.IP || current.NodeID != device.NodeID {
		return nil, errors.New("device revoked")
	}
	credential, err := s.ProxyCredential(device.UserID, device.ID)
	if err != nil || credential == "" {
		return nil, errors.New("missing device credential")
	}
	address := "http://" + net.JoinHostPort(device.IP, fmt.Sprint(s.options.AgentPort))
	if s.options.ProxyURL != nil {
		address = s.options.ProxyURL(device, "api")
	}
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid device address")
	}
	p, _ := url.Parse(route)
	u.Path, u.RawPath, u.RawQuery = p.Path, "", p.RawQuery
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Origin", s.options.Origin)
	r.Header.Set("X-Fleet-Device-ID", device.ID)
	r.Header.Set("X-Fleet-Device-Token", credential)
	transport, err := NewDeviceTransport(s.options.MeshProxy)
	if err != nil {
		return nil, err
	}
	// Close idle sockets after the body is consumed, not before returning it.
	transport.DisableKeepAlives = true
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client.Do(r)
}

func (s *Server) standbyHost(ctx context.Context, device Device, zone, date string) standbyHost {
	host := standbyHost{ID: device.ID, Name: device.Name, UsageStatus: "unavailable", Models: []standbyModel{}, Sessions: []standbySession{}, NodeID: device.NodeID, IP: device.IP}
	ctx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	response, err := s.standbyDeviceGET(ctx, device, "/api/usage/daily?timezone="+url.QueryEscape(zone))
	if err == nil {
		var usage standbyUsage
		if response.StatusCode == 404 {
			host.UsageStatus = "upgrade-required"
		}
		if response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&usage) == nil && usage.Date == date && usage.Timezone == zone && len(usage.Models) <= 256 {
			valid := true
			for _, m := range usage.Models {
				if m.InputTokens < 0 || m.OutputTokens < 0 || m.InputTokens > 1e15 || m.OutputTokens > 1e15 || m.TotalTokens != m.InputTokens+m.OutputTokens || len(m.Model) > 256 {
					valid = false
				}
			}
			if valid {
				host.Models = usage.Models
				host.UsageStatus = "ok"
				if usage.Partial {
					host.UsageStatus = "partial"
				}
			}
		}
		response.Body.Close()
	}
	for _, assistant := range []string{"codex", "claude", "dsh"} {
		cursor := ""
		for page := 0; page < 50; page++ {
			response, err = s.standbyDeviceGET(ctx, device, "/api/sessions?assistant="+assistant+"&limit=100&cursor="+url.QueryEscape(cursor))
			if err != nil {
				host.SessionsPartial = true
				break
			}
			if response.StatusCode == 501 {
				response.Body.Close()
				break
			} // assistant disabled
			if response.StatusCode != 200 {
				response.Body.Close()
				host.SessionsPartial = true
				break
			}
			err = s.applySessionReadStates(ctx, response, device.UserID, device.Index)
			var list struct {
				Sessions []struct {
					SessionID     string `json:"sessionId"`
					Title         string `json:"title"`
					Cwd           string `json:"cwd"`
					ProjectName   string `json:"projectName"`
					Status        string `json:"status"`
					Waiting       bool   `json:"waiting"`
					MarkedUnread  bool   `json:"markedUnread"`
					DesktopUnread *bool  `json:"desktopUnread"`
					Mtime         int64  `json:"mtime"`
					ReadAt        int64  `json:"readAt"`
					OutputEndedAt int64  `json:"outputEndedAt"`
				} `json:"sessions"`
				NextCursor string `json:"nextCursor"`
			}
			if err == nil {
				err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&list)
			}
			response.Body.Close()
			if err != nil {
				host.SessionsPartial = true
				break
			}
			for _, item := range list.Sessions {
				running := item.Status == "active" || item.Status == "running"
				activity := max(item.Mtime, item.OutputEndedAt)
				unread := item.MarkedUnread
				if !unread && !(item.ReadAt > 0 && item.ReadAt >= activity) {
					if item.DesktopUnread != nil {
						unread = *item.DesktopUnread
					} else {
						unread = item.ReadAt > 0 && activity > item.ReadAt
					}
				}
				if !running && !item.Waiting && !unread {
					continue
				}
				project := item.ProjectName
				if project == "" && item.Cwd != "" {
					project = path.Base(item.Cwd)
				}
				host.Sessions = append(host.Sessions, standbySession{ID: device.ID + "/" + assistant + "/" + item.SessionID, DeviceID: device.ID, DeviceName: device.Name, SessionID: item.SessionID, Assistant: assistant, Title: item.Title, Project: project, Running: running, Waiting: item.Waiting, Unread: unread, ActivityAt: activity})
			}
			if list.NextCursor == "" {
				break
			}
			if list.NextCursor == cursor || page == 49 {
				host.SessionsPartial = true
				break
			}
			cursor = list.NextCursor
		}
	}
	return host
}

func (s *Server) handleStandby(w http.ResponseWriter, r *http.Request, user User, valid func() bool) {
	if r.Method != http.MethodGet {
		reject(w, 405, "仅支持 GET")
		return
	}
	zone := r.URL.Query().Get("timezone")
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		reject(w, 400, "无效时区")
		return
	}
	date := s.now().In(loc).Format("2006-01-02")
	out := standbySnapshot{Version: 1, Date: date, Timezone: zone, GeneratedAt: s.now().UnixMilli(), Models: []standbyModel{}, Hosts: []standbyHost{}, Sessions: []standbySession{}, UsageComplete: true}
	devices, err := s.Devices(user.ID)
	if err != nil {
		s.failure(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 27*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ScopeContext(user.ID, ""), cancel)
	defer stop()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	results := make(chan standbyHost, len(devices))
	for _, d := range devices {
		if d.Status != "active" {
			continue
		}
		wg.Add(1)
		go func(device Device) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				results <- standbyHost{ID: device.ID, Name: device.Name, UsageStatus: "unavailable", SessionsPartial: true, NodeID: device.NodeID, IP: device.IP}
				return
			}
			results <- s.standbyHost(ctx, device, zone, date)
		}(d)
	}
	wg.Wait()
	close(results)
	models := map[string]standbyModel{}
	for host := range results {
		// Do not include a response received after device revocation/rebinding.
		if device, err := s.AuthorizedDevice(user.ID, host.ID); err != nil || device.NodeID != host.NodeID || device.IP != host.IP {
			continue
		}
		out.Hosts = append(out.Hosts, host)
		out.Sessions = append(out.Sessions, host.Sessions...)
		out.SessionsPartial = out.SessionsPartial || host.SessionsPartial
		if host.UsageStatus == "ok" {
			out.ReportingHosts++
		} else {
			out.UsageComplete = false
		}
		for _, m := range host.Models {
			key := m.Assistant + "/" + m.Model
			u := models[key]
			u.Assistant = m.Assistant
			u.Model = m.Model
			u.InputTokens += m.InputTokens
			u.OutputTokens += m.OutputTokens
			u.CachedInputTokens += m.CachedInputTokens
			u.CacheWriteTokens += m.CacheWriteTokens
			u.TotalTokens += m.TotalTokens
			models[key] = u
		}
	}
	if !valid() {
		reject(w, 401, "登录已失效")
		return
	}
	for _, m := range models {
		out.Models = append(out.Models, m)
		out.TotalTokens += m.TotalTokens
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].TotalTokens == out.Models[j].TotalTokens {
			return out.Models[i].Assistant+out.Models[i].Model < out.Models[j].Assistant+out.Models[j].Model
		}
		return out.Models[i].TotalTokens > out.Models[j].TotalTokens
	})
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].ID < out.Hosts[j].ID })
	sort.Slice(out.Sessions, func(i, j int) bool {
		if out.Sessions[i].Waiting != out.Sessions[j].Waiting {
			return out.Sessions[i].Waiting
		}
		if out.Sessions[i].ActivityAt == out.Sessions[j].ActivityAt {
			return out.Sessions[i].ID < out.Sessions[j].ID
		}
		return out.Sessions[i].ActivityAt > out.Sessions[j].ActivityAt
	})
	if len(out.Sessions) > 60 {
		out.Sessions = out.Sessions[:60]
	}
	respond(w, 200, out)
}
