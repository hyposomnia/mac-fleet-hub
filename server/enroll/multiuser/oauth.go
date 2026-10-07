package multiuser

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var oauthVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var oauthNoncePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

func validOAuthRedirect(value string) bool {
	redirect, err := url.Parse(value)
	if err != nil || redirect.Scheme != "http" || redirect.Hostname() != "127.0.0.1" || redirect.User != nil || redirect.Path != "/oauth/callback" || redirect.RawQuery != "" || redirect.ForceQuery || redirect.Fragment != "" || redirect.Opaque != "" {
		return false
	}
	port, err := strconv.Atoi(redirect.Port())
	return err == nil && port > 0 && port <= 65535 && redirect.RawPath == ""
}

func validOAuthParameters(values url.Values) bool {
	if len(values) != 8 {
		return false
	}
	for _, key := range []string{"client_id", "response_type", "scope", "redirect_uri", "state", "code_challenge_method", "code_challenge", "device_name"} {
		if len(values[key]) != 1 {
			return false
		}
	}
	challenge, err := base64.RawURLEncoding.DecodeString(values.Get("code_challenge"))
	return values.Get("client_id") == "fleet-hub" && values.Get("response_type") == "code" && values.Get("scope") == "device:enroll" &&
		validOAuthRedirect(values.Get("redirect_uri")) && oauthNoncePattern.MatchString(values.Get("state")) &&
		values.Get("code_challenge_method") == "S256" && err == nil && len(challenge) == 32 && base64.RawURLEncoding.EncodeToString(challenge) == values.Get("code_challenge") &&
		cleanName(values.Get("device_name")) != ""
}

func oauthError(writer http.ResponseWriter, message string) {
	respond(writer, http.StatusBadRequest, map[string]string{"error": message})
}

func (server *Server) oauthStart(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		reject(writer, 405, "仅支持 GET")
		return
	}
	parameters := request.URL.Query()
	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.allowedAuth(request, "oauth-start") {
		reject(writer, 429, "请求过于频繁")
		return
	}
	id := randomToken()
	_, err := server.db.Exec("INSERT INTO oauth_requests(id,challenge,redirect_uri,state,name,status,expires_at) VALUES(?,?,?,?,?,'pending',?)", id, parameters.Get("code_challenge"), parameters.Get("redirect_uri"), parameters.Get("state"), cleanName(parameters.Get("device_name")), server.now().Add(10*time.Minute).Unix())
	if err != nil {
		reject(writer, 409, "授权请求已使用，请从客户端重新发起")
		return
	}
	http.Redirect(writer, request, "/oauth/consent?request="+url.QueryEscape(id), http.StatusSeeOther)
}

func (server *Server) handleOAuthConsent(writer http.ResponseWriter, request *http.Request, user User) {
	var input struct {
		RequestID string `json:"request_id"`
		Action    string `json:"action"`
	}
	if request.Method == http.MethodGet && request.URL.Path == "/api/oauth/preview" {
		input.RequestID = request.URL.Query().Get("request")
	} else {
		if request.Method != http.MethodPost || request.URL.Path != "/api/oauth/authorize" {
			reject(writer, 405, "请求方法不支持")
			return
		}
		if !decode(writer, request, &input) {
			return
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	var name, redirect, state, status string
	var expires int64
	err := server.db.QueryRow("SELECT name,redirect_uri,state,status,expires_at FROM oauth_requests WHERE id=?", input.RequestID).Scan(&name, &redirect, &state, &status, &expires)
	if err != nil || status != "pending" || expires <= server.now().Unix() {
		reject(writer, 410, "授权已过期或已处理，请从客户端重新发起")
		return
	}
	if request.Method == http.MethodGet {
		respond(writer, 200, map[string]string{"name": name, "owner_email": user.Email, "redirect_uri": redirect})
		return
	}
	callback, _ := url.Parse(redirect)
	parameters := callback.Query()
	parameters.Set("state", state)
	switch input.Action {
	case "approve":
		code := randomToken()
		_, err = server.db.Exec("UPDATE oauth_requests SET status='approved',user_id=?,code_hash=?,expires_at=? WHERE id=? AND status='pending'", user.ID, digest(code), server.now().Add(2*time.Minute).Unix(), input.RequestID)
		parameters.Set("code", code)
	case "deny":
		_, err = server.db.Exec("UPDATE oauth_requests SET status='denied' WHERE id=?", input.RequestID)
		parameters.Set("error", "access_denied")
	default:
		oauthError(writer, "invalid_request")
		return
	}
	if err != nil {
		server.failure(writer, err)
		return
	}
	callback.RawQuery = parameters.Encode()
	respond(writer, 200, map[string]string{"redirect_uri": callback.String()})
}

func (server *Server) handleOAuthToken(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		reject(writer, 405, "仅支持 POST")
		return
	}
	if origin := request.Header.Get("Origin"); origin != "" && origin != server.options.Origin {
		reject(writer, 403, "请求来源不合法")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 8192)
	if request.ParseForm() != nil || len(request.PostForm) != 5 || request.URL.RawQuery != "" {
		oauthError(writer, "invalid_request")
		return
	}
	for _, key := range []string{"grant_type", "client_id", "code", "code_verifier", "redirect_uri"} {
		if len(request.PostForm[key]) != 1 {
			oauthError(writer, "invalid_request")
			return
		}
	}
	parameters := request.PostForm
	if parameters.Get("grant_type") != "authorization_code" || parameters.Get("client_id") != "fleet-hub" || !oauthVerifierPattern.MatchString(parameters.Get("code_verifier")) || !validOAuthRedirect(parameters.Get("redirect_uri")) {
		oauthError(writer, "invalid_request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.allowedAuth(request, "oauth-token") {
		reject(writer, 429, "请求过于频繁")
		return
	}
	var id, challenge, redirect, name, status string
	var owner, expires int64
	err := server.db.QueryRow("SELECT id,challenge,redirect_uri,name,status,user_id,expires_at FROM oauth_requests WHERE code_hash=?", digest(parameters.Get("code"))).Scan(&id, &challenge, &redirect, &name, &status, &owner, &expires)
	calculated := sha256.Sum256([]byte(parameters.Get("code_verifier")))
	if err != nil || status != "approved" || expires <= server.now().Unix() || redirect != parameters.Get("redirect_uri") || challenge != base64.RawURLEncoding.EncodeToString(calculated[:]) {
		oauthError(writer, "invalid_grant")
		return
	}
	user, err := server.user(owner)
	if err != nil || user.Status != "active" || !user.TOTPBound {
		oauthError(writer, "invalid_grant")
		return
	}
	transaction, err := server.db.Begin()
	if err != nil {
		server.failure(writer, err)
		return
	}
	defer transaction.Rollback()
	result, err := transaction.Exec("INSERT INTO devices(user_id,name,status,created_at) VALUES(?,?,'installing',?)", owner, name, server.now().Unix())
	if err != nil {
		server.failure(writer, err)
		return
	}
	index, err := result.LastInsertId()
	if err != nil {
		server.failure(writer, err)
		return
	}
	enrollment, token := randomToken(), randomToken()
	_, err = transaction.Exec("INSERT INTO enrollments(id,code,claim_hash,user_id,device_index,name,state,expires_at) VALUES(?,?,?,?,?,?,'confirmed',?)", enrollment, "oauth_"+randomToken(), digest(token), owner, index, name, server.now().Add(10*time.Minute).Unix())
	if err == nil {
		_, err = transaction.Exec("UPDATE oauth_requests SET status='consumed',code_hash=NULL WHERE id=? AND status='approved'", id)
	}
	if err == nil {
		err = transaction.Commit()
	}
	if err != nil {
		server.failure(writer, err)
		return
	}
	server.audit(owner, owner, "device_oauth_authorized:m"+intString(index))
	respond(writer, 200, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 600, "scope": "device:enroll", "enrollment_id": enrollment})
}
