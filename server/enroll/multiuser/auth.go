package multiuser

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"image/png"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/argon2"
)

const cookieName = "fleet_session"

type sessionState struct {
	Hash, CSRF, Stage string
	UserID, Expires   int64
	Pending           []byte
}

func normalizeEmail(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && len(value) <= 254 && strings.Contains(value, "@")
}
func validPassword(value string) bool { return len(value) >= 12 && len(value) <= 256 }
func hashPassword(value string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	hash := argon2.IDKey([]byte(value), salt, 3, 64*1024, 4, 32)
	return "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
}
func checkPassword(value, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 3 || parts[0] != "argon2id" || len(value) > 256 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(want) != 32 {
		return false
	}
	actual := argon2.IDKey([]byte(value), salt, 3, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(actual, want) == 1
}

func (server *Server) newSession(writer http.ResponseWriter, request *http.Request, userID int64, stage string) error {
	if cookie, err := request.Cookie(cookieName); err == nil {
		server.db.Exec("DELETE FROM sessions WHERE token_hash=?", digest(cookie.Value))
	}
	token := randomToken()
	duration := 15 * time.Minute
	if stage == "full" {
		duration = 30 * 24 * time.Hour
	}
	now := server.now()
	ip, _, _ := net.SplitHostPort(request.RemoteAddr)
	_, err := server.db.Exec("INSERT INTO sessions(token_hash,user_id,csrf,stage,created_at,expires_at,last_seen,agent,ip) VALUES(?,?,?,?,?,?,?,?,?)", digest(token), userID, randomToken(), stage, now.Unix(), now.Add(duration).Unix(), now.Unix(), request.UserAgent(), ip)
	if err != nil {
		return err
	}
	http.SetCookie(writer, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(server.options.Origin, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: int(duration.Seconds()), Expires: now.Add(duration)})
	return nil
}

func (server *Server) authenticate(request *http.Request, full bool) (sessionState, User, error) {
	var session sessionState
	cookie, err := request.Cookie(cookieName)
	if err != nil {
		return session, User{}, err
	}
	err = server.db.QueryRow("SELECT token_hash,user_id,csrf,stage,expires_at,pending_secret FROM sessions WHERE token_hash=?", digest(cookie.Value)).Scan(&session.Hash, &session.UserID, &session.CSRF, &session.Stage, &session.Expires, &session.Pending)
	if err != nil || session.Expires <= server.now().Unix() || (full && session.Stage != "full") {
		return session, User{}, errors.New("invalid session")
	}
	user, err := server.user(session.UserID)
	if err != nil || user.Status == "disabled" || (full && user.Status != "active") {
		return session, User{}, errors.New("invalid user")
	}
	server.db.Exec("UPDATE sessions SET last_seen=? WHERE token_hash=?", server.now().Unix(), session.Hash)
	return session, user, nil
}

func (server *Server) newTOTP(email string) (*otp.Key, []byte, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "mac-fleet-hub", AccountName: email, SecretSize: 20})
	if err != nil {
		return nil, nil, err
	}
	sealed, err := server.seal(key.Secret())
	return key, sealed, err
}
func (server *Server) setupResponse(writer http.ResponseWriter, user User, secret string) {
	uri := totpURI(user.Email, secret)
	respond(writer, 200, map[string]any{"state": "setup", "user": user, "totp": map[string]string{"secret": secret, "uri": uri}})
}

func totpURI(email, secret string) string {
	uri := url.URL{Scheme: "otpauth", Host: "totp", Path: "/mac-fleet-hub:" + email}
	query := url.Values{"secret": {secret}, "issuer": {"mac-fleet-hub"}, "period": {"30"}, "digits": {"6"}}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func (server *Server) allowedAuth(request *http.Request, email string) bool {
	ip, _, _ := net.SplitHostPort(request.RemoteAddr)
	key := ip + "/" + digest(email)
	server.failMu.Lock()
	defer server.failMu.Unlock()
	now := server.now()
	for existing, times := range server.failures {
		if len(times) == 0 || now.Sub(times[len(times)-1]) > 15*time.Minute {
			delete(server.failures, existing)
		}
	}
	times := server.failures[key]
	if len(times) == 0 && len(server.failures) >= 4096 {
		return false
	}
	if len(times) >= 8 && now.Sub(times[len(times)-8]) < 15*time.Minute {
		return false
	}
	server.failures[key] = append(times, now)
	return true
}

func (server *Server) clearAuthFailures(request *http.Request, identity string) {
	ip, _, _ := net.SplitHostPort(request.RemoteAddr)
	server.failMu.Lock()
	delete(server.failures, ip+"/"+digest(identity))
	server.failMu.Unlock()
}

func (server *Server) handleAuth(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/auth/")
	if path == "register" || path == "login" || path == "recover" {
		if request.Method != "POST" {
			reject(writer, 405, "仅支持 POST")
			return
		}
		select {
		case server.authSlots <- struct{}{}:
			defer func() { <-server.authSlots }()
		default:
			reject(writer, 429, "请稍后重试")
			return
		}
		server.handleCredentials(writer, request, path)
		return
	}
	session, user, err := server.authenticate(request, false)
	if err != nil {
		reject(writer, 401, "请重新登录")
		return
	}
	if path == "verify" {
		if request.Method != "POST" || session.Stage == "full" {
			reject(writer, 400, "没有待验证登录")
			return
		}
		if !server.allowedAuth(request, "verify/"+user.Email) {
			reject(writer, 429, "尝试次数过多")
			return
		}
		server.verifyLogin(writer, request, session, user)
		return
	}
	if path == "qr" && request.Method == "GET" {
		server.handleQR(writer, request, session, user)
		return
	}
	if session.Stage != "full" || user.Status != "active" {
		reject(writer, 401, "请完成验证器绑定和登录")
		return
	}
	if unsafeMethod(request.Method) && (request.Header.Get("X-CSRF-Token") != session.CSRF) {
		reject(writer, 403, "安全令牌不合法")
		return
	}
	if path == "me" && request.Method == "GET" {
		respond(writer, 200, map[string]any{"user": user, "csrf_token": session.CSRF})
		return
	}
	if path == "sessions" && request.Method == "GET" {
		server.handleSessions(writer, session)
		return
	}
	if request.Method != "POST" {
		reject(writer, 405, "仅支持 POST")
		return
	}
	if path == "password" || strings.HasPrefix(path, "totp/") || path == "recovery-codes" {
		select {
		case server.authSlots <- struct{}{}:
			defer func() { <-server.authSlots }()
		default:
			reject(writer, 429, "请稍后重试")
			return
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	session, user, err = server.authenticate(request, true)
	if err != nil {
		reject(writer, 401, "请重新登录")
		return
	}
	switch path {
	case "logout":
		server.db.Exec("DELETE FROM sessions WHERE token_hash=?", session.Hash)
		server.cancelSession(session.Hash)
		http.SetCookie(writer, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(server.options.Origin, "https:"), SameSite: http.SameSiteLaxMode})
		respond(writer, 200, map[string]bool{"ok": true})
	case "logout-others":
		if _, err = server.db.Exec("DELETE FROM sessions WHERE user_id=? AND token_hash!=?", user.ID, session.Hash); err != nil {
			server.failure(writer, err)
			return
		}
		server.cancelOtherSessions(user.ID, session.Hash)
		respond(writer, 200, map[string]bool{"ok": true})
	case "password", "totp/start", "totp/confirm", "recovery-codes":
		server.handleSecurity(writer, request, path, session, user)
	default:
		reject(writer, 404, "接口不存在")
	}
}

func (server *Server) handleCredentials(writer http.ResponseWriter, request *http.Request, path string) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Confirm  string `json:"confirm_password"`
		Recovery string `json:"recovery_code"`
	}
	if !decode(writer, request, &input) {
		return
	}
	email := normalizeEmail(input.Email)
	if !server.allowedAuth(request, email) {
		reject(writer, 429, "尝试次数过多，请稍后重试")
		return
	}
	if path == "register" {
		if !validEmail(email) || !validPassword(input.Password) || input.Password != input.Confirm {
			reject(writer, 400, "请输入有效邮箱及至少 12 字符的密码，两次密码必须一致")
			return
		}
		key, sealed, err := server.newTOTP(email)
		if err != nil {
			server.failure(writer, err)
			return
		}
		encoded := hashPassword(input.Password)
		server.mu.Lock()
		defer server.mu.Unlock()
		var existing int64
		if server.db.QueryRow("SELECT id FROM users WHERE email=?", email).Scan(&existing) == nil {
			reject(writer, 409, "该邮箱已注册")
			return
		}
		result, err := server.db.Exec("INSERT INTO users(email,password,totp,created_at) VALUES(?,?,?,?)", email, encoded, sealed, server.now().Unix())
		if err != nil {
			server.failure(writer, err)
			return
		}
		id, _ := result.LastInsertId()
		user, _ := server.user(id)
		if err = server.newSession(writer, request, id, "setup"); err != nil {
			server.failure(writer, err)
			return
		}
		server.setupResponse(writer, user, key.Secret())
		server.clearAuthFailures(request, email)
		return
	}
	var id int64
	var encoded, status string
	var sealed []byte
	err := server.db.QueryRow("SELECT id,password,status,totp FROM users WHERE email=?", email).Scan(&id, &encoded, &status, &sealed)
	if err != nil || status == "disabled" || !checkPassword(input.Password, encoded) {
		reject(writer, 401, "账号或凭据不正确")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	user, err := server.user(id)
	var currentEncoded string
	passwordErr := server.db.QueryRow("SELECT password FROM users WHERE id=?", id).Scan(&currentEncoded)
	if err != nil || user.Status == "disabled" || passwordErr != nil || currentEncoded != encoded {
		reject(writer, 401, "账号或凭据不正确")
		return
	}
	if path == "recover" {
		transaction, err := server.db.Begin()
		if err != nil {
			server.failure(writer, err)
			return
		}
		defer transaction.Rollback()
		result, err := transaction.Exec("DELETE FROM recovery_codes WHERE user_id=? AND hash=?", id, digest(strings.TrimSpace(input.Recovery)))
		if err != nil {
			server.failure(writer, err)
			return
		}
		count, _ := result.RowsAffected()
		if count != 1 {
			reject(writer, 401, "账号或凭据不正确")
			return
		}
		_, newSealed, err := server.newTOTP(email)
		if err != nil {
			server.failure(writer, err)
			return
		}
		if _, err = transaction.Exec("DELETE FROM sessions WHERE user_id=?", id); err == nil {
			_, err = transaction.Exec("UPDATE users SET status='setup',totp=?,totp_confirmed=0,totp_step=-1 WHERE id=?", newSealed, id)
		}
		if err != nil {
			server.failure(writer, err)
			return
		}
		if err = transaction.Commit(); err != nil {
			server.failure(writer, err)
			return
		}
		sealed = newSealed
		user.Status = "setup"
		server.cancelScopes(id, "")
		server.audit(id, id, "account_recovery")
		ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
		defer cancel()
		if err = server.syncNetwork(ctx); err != nil {
			reject(writer, 503, "账号已进入恢复状态，网络策略等待重试；请重新登录")
			return
		}
	}
	stage := "challenge"
	if user.Status == "setup" {
		stage = "setup"
	}
	if err = server.newSession(writer, request, id, stage); err != nil {
		server.failure(writer, err)
		return
	}
	if stage == "setup" {
		secret, err := server.unseal(sealed)
		if err != nil {
			server.failure(writer, err)
			return
		}
		server.setupResponse(writer, user, secret)
	} else {
		respond(writer, 200, map[string]string{"state": "challenge"})
	}
	server.clearAuthFailures(request, email)
}

func (server *Server) consumeCode(userID int64, code string) (bool, error) {
	var sealed []byte
	var last int64
	if err := server.db.QueryRow("SELECT totp,totp_step FROM users WHERE id=?", userID).Scan(&sealed, &last); err != nil {
		return false, err
	}
	secret, err := server.unseal(sealed)
	if err != nil {
		return false, err
	}
	step := server.now().Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		candidate := step + offset
		if candidate <= last {
			continue
		}
		valid, err := totp.ValidateCustom(strings.TrimSpace(code), secret, time.Unix(candidate*30, 0), totp.ValidateOpts{Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			return false, err
		}
		if valid {
			result, err := server.db.Exec("UPDATE users SET totp_step=? WHERE id=? AND totp_step<?", candidate, userID, candidate)
			if err != nil {
				return false, err
			}
			count, _ := result.RowsAffected()
			return count == 1, nil
		}
	}
	return false, nil
}

func (server *Server) recoveryCodes(userID int64) ([]string, error) {
	transaction, err := server.db.Begin()
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	if _, err = transaction.Exec("DELETE FROM recovery_codes WHERE user_id=?", userID); err != nil {
		return nil, err
	}
	codes := []string{}
	for index := 0; index < 10; index++ {
		code := randomToken()[:20]
		if _, err = transaction.Exec("INSERT INTO recovery_codes(user_id,hash) VALUES(?,?)", userID, digest(code)); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, transaction.Commit()
}

func (server *Server) verifyLogin(writer http.ResponseWriter, request *http.Request, session sessionState, user User) {
	var input struct {
		Code string `json:"code"`
	}
	if !decode(writer, request, &input) {
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	current, currentUser, err := server.authenticate(request, false)
	if err != nil || current.Stage == "full" {
		reject(writer, 401, "验证已失效")
		return
	}
	user = currentUser
	session = current
	valid, err := server.consumeCode(user.ID, input.Code)
	if err != nil {
		server.failure(writer, err)
		return
	}
	if !valid {
		reject(writer, 401, "验证码错误或已使用")
		return
	}
	var codes []string
	if user.Status == "setup" {
		codes, err = server.recoveryCodes(user.ID)
		if err != nil {
			server.failure(writer, err)
			return
		}
	}
	if _, err = server.db.Exec("UPDATE users SET status='active',totp_confirmed=1,last_login=? WHERE id=? AND status!='disabled'", server.now().Unix(), user.ID); err != nil {
		server.failure(writer, err)
		return
	}
	if err = server.newSession(writer, request, user.ID, "full"); err != nil {
		server.failure(writer, err)
		return
	}
	user, _ = server.user(user.ID)
	server.audit(user.ID, user.ID, "login")
	respond(writer, 200, map[string]any{"user": user, "recovery_codes": codes})
	server.clearAuthFailures(request, "verify/"+user.Email)
}

func (server *Server) handleQR(writer http.ResponseWriter, request *http.Request, session sessionState, user User) {
	sealed := session.Pending
	if len(sealed) == 0 {
		if session.Stage != "setup" {
			reject(writer, 403, "没有待绑定验证器")
			return
		}
		if err := server.db.QueryRow("SELECT totp FROM users WHERE id=?", user.ID).Scan(&sealed); err != nil {
			server.failure(writer, err)
			return
		}
	}
	secret, err := server.unseal(sealed)
	if err != nil {
		server.failure(writer, err)
		return
	}
	key, err := otp.NewKeyFromURL(totpURI(user.Email, secret))
	if err != nil {
		server.failure(writer, err)
		return
	}
	image, err := key.Image(256, 256)
	if err != nil {
		server.failure(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "image/png")
	png.Encode(writer, image)
}

func (server *Server) handleSessions(writer http.ResponseWriter, current sessionState) {
	rows, err := server.db.Query("SELECT token_hash,created_at,expires_at,last_seen,agent,ip FROM sessions WHERE user_id=? AND stage='full' AND expires_at>? ORDER BY created_at DESC", current.UserID, server.now().Unix())
	if err != nil {
		server.failure(writer, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var hash, agent, ip string
		var created, expires, last int64
		if err = rows.Scan(&hash, &created, &expires, &last, &agent, &ip); err != nil {
			server.failure(writer, err)
			return
		}
		items = append(items, map[string]any{"id": hash[:12], "created_at": created, "expires_at": expires, "last_seen": last, "agent": agent, "ip": ip, "current": hash == current.Hash})
	}
	respond(writer, 200, map[string]any{"sessions": items})
}

func (server *Server) handleSecurity(writer http.ResponseWriter, request *http.Request, path string, session sessionState, user User) {
	var input struct {
		Password string `json:"password"`
		Current  string `json:"current_password"`
		Confirm  string `json:"confirm_password"`
		Code     string `json:"code"`
	}
	if !decode(writer, request, &input) {
		return
	}
	if !server.allowedAuth(request, "security/"+user.Email) {
		reject(writer, 429, "尝试次数过多")
		return
	}
	if path == "totp/confirm" {
		if len(session.Pending) == 0 {
			reject(writer, 400, "请先申请更换验证器")
			return
		}
		secret, err := server.unseal(session.Pending)
		if err != nil {
			server.failure(writer, err)
			return
		}
		valid, validationErr := totp.ValidateCustom(input.Code, secret, server.now(), totp.ValidateOpts{Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if validationErr != nil || !valid {
			reject(writer, 401, "验证码错误")
			return
		}
		if _, err = server.db.Exec("UPDATE users SET totp=?,totp_step=? WHERE id=?", session.Pending, server.now().Unix()/30, user.ID); err != nil {
			server.failure(writer, err)
			return
		}
		codes, err := server.recoveryCodes(user.ID)
		if err != nil {
			server.failure(writer, err)
			return
		}
		server.revokeUserSessions(user.ID)
		server.audit(user.ID, user.ID, "totp_changed")
		server.clearAuthFailures(request, "security/"+user.Email)
		respond(writer, 200, map[string]any{"recovery_codes": codes, "login_required": true})
		return
	}
	password := input.Password
	if path == "password" {
		password = input.Current
		if !validPassword(input.Password) || input.Password != input.Confirm {
			reject(writer, 400, "新密码至少 12 字符，两次输入必须一致")
			return
		}
	}
	var encoded string
	if err := server.db.QueryRow("SELECT password FROM users WHERE id=?", user.ID).Scan(&encoded); err != nil {
		server.failure(writer, err)
		return
	}
	if !checkPassword(password, encoded) {
		reject(writer, 401, "凭据不正确")
		return
	}
	valid, err := server.consumeCode(user.ID, input.Code)
	if err != nil {
		server.failure(writer, err)
		return
	}
	if !valid {
		reject(writer, 401, "验证码错误或已使用")
		return
	}
	server.clearAuthFailures(request, "security/"+user.Email)
	switch path {
	case "password":
		if _, err = server.db.Exec("UPDATE users SET password=? WHERE id=?", hashPassword(input.Password), user.ID); err != nil {
			server.failure(writer, err)
			return
		}
		server.revokeUserSessions(user.ID)
		server.audit(user.ID, user.ID, "password_changed")
		respond(writer, 200, map[string]bool{"login_required": true})
	case "totp/start":
		key, sealed, err := server.newTOTP(user.Email)
		if err != nil {
			server.failure(writer, err)
			return
		}
		if _, err = server.db.Exec("UPDATE sessions SET pending_secret=? WHERE token_hash=?", sealed, session.Hash); err != nil {
			server.failure(writer, err)
			return
		}
		respond(writer, 200, map[string]string{"secret": key.Secret(), "uri": key.URL()})
	case "recovery-codes":
		codes, err := server.recoveryCodes(user.ID)
		if err != nil {
			server.failure(writer, err)
			return
		}
		server.audit(user.ID, user.ID, "recovery_codes_changed")
		respond(writer, 200, map[string]any{"recovery_codes": codes})
	}
}

func (server *Server) revokeUserSessions(userID int64) error {
	_, err := server.db.Exec("DELETE FROM sessions WHERE user_id=?", userID)
	server.cancelScopes(userID, "")
	return err
}
func (server *Server) cancelSession(hash string) {
	server.scopeMu.Lock()
	defer server.scopeMu.Unlock()
	for key, existing := range server.scopes {
		if strings.HasPrefix(key, "session/") && strings.HasSuffix(key, "/"+hash) {
			existing.cancel()
			delete(server.scopes, key)
		}
	}
}
func (server *Server) cancelOtherSessions(userID int64, except string) {
	server.scopeMu.Lock()
	defer server.scopeMu.Unlock()
	for key, value := range server.scopes {
		if strings.HasPrefix(key, "session/"+intString(userID)+"/") && key != "session/"+intString(userID)+"/"+except {
			value.cancel()
			delete(server.scopes, key)
		}
	}
}
