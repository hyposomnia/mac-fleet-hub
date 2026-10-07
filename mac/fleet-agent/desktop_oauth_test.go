package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopOAuthUsesPKCEAndRejectsStaleState(t *testing.T) {
	var authorization url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" {
			writer.WriteHeader(404)
			return
		}
		request.ParseForm()
		challenge := sha256.Sum256([]byte(request.PostForm.Get("code_verifier")))
		if request.PostForm.Get("code") != "server-authorization-code" || base64.RawURLEncoding.EncodeToString(challenge[:]) != authorization.Get("code_challenge") || request.PostForm.Get("redirect_uri") != authorization.Get("redirect_uri") {
			t.Error("OAuth token request lost PKCE or redirect binding")
		}
		json.NewEncoder(writer).Encode(map[string]any{"access_token": strings.Repeat("t", 48), "token_type": "Bearer", "scope": "device:enroll", "expires_in": 600, "enrollment_id": "enrollment"})
	}))
	defer server.Close()
	client := deviceHTTPClient()
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var callback string
	pending, err := desktopOAuthAuthorization(ctx, client, server.URL, func(link, code string) {
		browser, _ := url.Parse(link)
		authorization = browser.Query()
		if browser.Path != "/oauth/authorize" || code != "" || authorization.Get("code_challenge_method") != "S256" {
			t.Error("not browser OAuth")
		}
		callback = authorization.Get("redirect_uri")
		go func() {
			response, err := http.Get(callback + "?state=stale&code=stolen")
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
			if response.StatusCode != 400 {
				t.Error("accepted stale state")
			}
			response, err = http.Get(callback + "?state=" + authorization.Get("state") + "&code=server-authorization-code")
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
		}()
	})
	if err != nil || pending.ID != "enrollment" || pending.Token != strings.Repeat("t", 48) || pending.Code != "" {
		t.Fatalf("authorization: %+v %v", pending, err)
	}
	if response, err := http.Get(callback); err == nil {
		response.Body.Close()
		t.Fatal("callback listener survived exchange")
	}
}

func TestDesktopOAuthEveryAttemptIsNewAndCancellationClosesListener(t *testing.T) {
	var previousState, previousChallenge string
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		client := deviceHTTPClient()
		var callback string
		_, err := desktopOAuthAuthorization(ctx, client, "https://fleet.example.test", func(link, code string) {
			browser, _ := url.Parse(link)
			parameters := browser.Query()
			if parameters.Get("state") == previousState || parameters.Get("code_challenge") == previousChallenge {
				t.Error("reused expired authorization")
			}
			previousState, previousChallenge = parameters.Get("state"), parameters.Get("code_challenge")
			callback = parameters.Get("redirect_uri")
			cancel()
		})
		client.CloseIdleConnections()
		if err == nil {
			t.Fatal("cancellation authorized a device")
		}
		if response, err := http.Get(callback); err == nil {
			response.Body.Close()
			t.Fatal("cancelled listener remained open")
		}
	}
}

func TestDesktopOAuthCallbackRejectsAmbiguousParametersAndHandlesDenial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := deviceHTTPClient()
	defer client.CloseIdleConnections()
	finished := make(chan struct{})
	_, err := desktopOAuthAuthorization(ctx, client, "https://fleet.example.test", func(link, _ string) {
		browser, _ := url.Parse(link)
		parameters := browser.Query()
		callback, state := parameters.Get("redirect_uri"), parameters.Get("state")
		go func() {
			defer close(finished)
			for _, query := range []string{
				"state=" + state + "&code=one&unexpected=value",
				"state=" + state + "&code=one&code=two",
				"state=" + state + "&code=one&error=access_denied",
				"state=" + state + "&code=one&bad=%",
			} {
				response, callError := http.Get(callback + "?" + query)
				if callError != nil {
					t.Error(callError)
					return
				}
				response.Body.Close()
				if response.StatusCode != 400 {
					t.Errorf("ambiguous callback accepted: %d", response.StatusCode)
					cancel()
					return
				}
			}
			response, callError := http.Get(callback + "?state=" + state + "&error=access_denied")
			if callError != nil {
				t.Error(callError)
				return
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Error("denial callback rejected")
			}
		}()
	})
	<-finished
	if err == nil || !strings.Contains(err.Error(), "取消") {
		t.Fatalf("denial must be actionable: %v", err)
	}
}

func TestDesktopOAuthIntegrationProcess(t *testing.T) {
	if os.Getenv("FLEET_OAUTH_INTEGRATION") != "1" {
		t.Skip("isolated native OAuth process helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	path := os.Getenv("FLEET_BINDING_FILE")
	if err := writePrivateJSON(filepath.Join(filepath.Dir(path), "pairing.json"), pairingPending{Origin: "https://expired.example.test", ID: "expired", Token: "expired", URL: "https://expired.example.test/enroll/confirm", Code: "OLD"}); err != nil {
		t.Fatal(err)
	}
	coordinator := newDesktopPairing(loginOptions{Path: path,
		Authorize: func(scope context.Context, client *http.Client, origin string, browser func(string, string)) (pairingPending, error) {
			return desktopOAuthAuthorization(scope, client, origin, func(link, code string) {
				browser(link, code)
				if err := os.WriteFile(os.Getenv("FLEET_OAUTH_BROWSER_FILE"), []byte(link), 0600); err != nil {
					t.Error(err)
				}
			})
		},
		Join:  func(context.Context, pairingGrant, bool) error { return nil },
		Setup: func(context.Context, deviceBinding) error { return nil },
	})
	defer coordinator.Cancel()
	if err := coordinator.Start(os.Getenv("FLEET_OAUTH_ORIGIN")); err != nil {
		t.Fatal(err)
	}
	for {
		state := coordinator.Snapshot()
		if state.Code != "" || state.Phase == "awaiting_confirmation" {
			t.Fatal("native OAuth reused CLI pairing")
		}
		if state.Phase == "complete" {
			break
		}
		if state.Phase == "failed" {
			t.Fatal(state.Error)
		}
		select {
		case <-ctx.Done():
			t.Fatal("OAuth process timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
	binding, err := readDeviceBinding(path)
	if err != nil || !binding.Complete {
		t.Fatal("native OAuth did not activate binding")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(path), "pairing.json")); !os.IsNotExist(err) {
		t.Fatal("pending credentials were not removed")
	}
}
