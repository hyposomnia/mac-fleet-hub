package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func desktopOAuthAuthorization(ctx context.Context, client *http.Client, origin string, browser func(string, string)) (pairingPending, error) {
	var pending pairingPending
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return pending, errors.New("无法打开本机授权回调")
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/oauth/callback"
	var verifierBytes, stateBytes [32]byte
	if _, err = rand.Read(verifierBytes[:]); err != nil {
		return pending, err
	}
	if _, err = rand.Read(stateBytes[:]); err != nil {
		return pending, err
	}
	verifier, state := base64.RawURLEncoding.EncodeToString(verifierBytes[:]), base64.RawURLEncoding.EncodeToString(stateBytes[:])
	challenge := sha256.Sum256([]byte(verifier))
	name, _ := os.Hostname()
	if name == "" {
		name = "Mac"
	}
	parameters := url.Values{"client_id": {"fleet-hub"}, "response_type": {"code"}, "scope": {"device:enroll"},
		"redirect_uri": {redirect}, "state": {state}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "device_name": {name}}
	type result struct {
		code   string
		denied bool
	}
	completed := make(chan result, 1)
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		values, queryError := url.ParseQuery(request.URL.RawQuery)
		if queryError != nil || len(values) != 2 || request.Method != http.MethodGet || request.Host != listener.Addr().String() || request.URL.Path != "/oauth/callback" || request.URL.RawPath != "" || len(values["state"]) != 1 || subtle.ConstantTimeCompare([]byte(values.Get("state")), []byte(state)) != 1 ||
			(len(values["code"]) != 1 || values.Get("code") == "") && (len(values["error"]) != 1 || values.Get("error") != "access_denied") || values.Has("code") && values.Has("error") {
			http.Error(writer, "无效授权回调", http.StatusBadRequest)
			return
		}
		select {
		case completed <- result{code: values.Get("code"), denied: values.Get("error") != ""}:
			writer.Write([]byte("授权已返回 Fleet Hub，可以关闭此页面。"))
		default:
			http.Error(writer, "授权回调已处理", http.StatusConflict)
		}
	})
	defer server.Close()
	go server.Serve(listener)
	if browser == nil {
		return pending, errors.New("浏览器授权入口未就绪")
	}
	browser(origin+"/oauth/authorize?"+parameters.Encode(), "")
	var callback result
	select {
	case <-ctx.Done():
		return pending, ctx.Err()
	case callback = <-completed:
	}
	shutdown, stop := context.WithTimeout(context.Background(), time.Second)
	server.Shutdown(shutdown)
	stop()
	if callback.denied {
		return pending, errors.New("网页授权已取消")
	}
	form := url.Values{"client_id": {"fleet-hub"}, "grant_type": {"authorization_code"}, "code": {callback.code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return pending, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return pending, errors.New("授权兑换失败，请重新打开网页授权")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return pending, errors.New("网页授权已过期或被拒绝，请重新发起")
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		EnrollmentID string `json:"enrollment_id"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 32<<10)).Decode(&token); err != nil || len(token.AccessToken) < 40 || token.EnrollmentID == "" || token.TokenType != "Bearer" || token.Scope != "device:enroll" {
		return pending, errors.New("服务器授权响应不完整")
	}
	return pairingPending{Origin: origin, ID: token.EnrollmentID, Token: token.AccessToken}, nil
}
