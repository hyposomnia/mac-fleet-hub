package multiuser

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func normalizeAuthOptions(o *Options) error {
	if o.SessionLifetime == 0 {
		o.SessionLifetime = 30 * 24 * time.Hour
	}
	if o.AuthWindow == 0 {
		o.AuthWindow = 15 * time.Minute
	}
	if o.AuthAttempts == 0 {
		o.AuthAttempts = 8
	}
	if o.AuthConcurrency == 0 {
		o.AuthConcurrency = 4
	}
	if o.SessionLifetime < time.Minute || o.AuthWindow < time.Second || o.AuthAttempts < 1 || o.AuthConcurrency < 1 {
		return errors.New("invalid authentication policy")
	}
	if o.TrustedProxies == nil {
		o.TrustedProxies = []string{"127.0.0.0/8", "::1/128"}
	}
	for _, cidr := range o.TrustedProxies {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid trusted proxy %q: %w", cidr, err)
		}
	}
	return nil
}
func (s *Server) trustedProxy(ip net.IP) bool {
	for _, cidr := range s.options.TrustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if !s.trustedProxy(ip) {
		return ip.String()
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(chain) - 1; i >= 0; i-- {
		next := net.ParseIP(strings.TrimSpace(chain[i]))
		if next == nil {
			return ip.String()
		}
		ip = next
		if !s.trustedProxy(ip) {
			break
		}
	}
	return ip.String()
}
func (s *Server) rejectAuthLimit(w http.ResponseWriter, r *http.Request, identity string) {
	s.failMu.Lock()
	times := s.failures[s.clientIP(r)+"/"+digest(identity)]
	seconds := 1
	if len(times) >= s.options.AuthAttempts {
		seconds = int(times[len(times)-s.options.AuthAttempts].Add(s.options.AuthWindow).Sub(s.now()).Seconds()) + 1
	}
	s.failMu.Unlock()
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	reject(w, 429, fmt.Sprintf("尝试次数过多，请在 %d 秒后重试", seconds))
}
func (s *Server) renewSession(w http.ResponseWriter, r *http.Request, session sessionState) {
	if !s.options.SessionSliding || session.Stage != "full" || time.Unix(session.Expires, 0).Sub(s.now()) > s.options.SessionLifetime/2 {
		return
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return
	}
	expiry := s.now().Add(s.options.SessionLifetime)
	result, err := s.db.Exec("UPDATE sessions SET expires_at=? WHERE token_hash=? AND expires_at>?", expiry.Unix(), session.Hash, s.now().Unix())
	if err != nil {
		return
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: cookie.Value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.options.Origin, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: int(s.options.SessionLifetime.Seconds()), Expires: expiry})
}
