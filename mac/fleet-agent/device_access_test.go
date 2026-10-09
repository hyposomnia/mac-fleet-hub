package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDeviceBindingPrivateStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	binding := deviceBinding{Origin: "https://fleet.example.com", DeviceID: "m101", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43), OwnerEmail: "owner@example.com"}
	if err := writePrivateJSON(path, binding); err != nil {
		t.Fatal(err)
	}
	if actual, err := readDeviceBinding(path); err != nil || actual.DeviceID != binding.DeviceID {
		t.Fatal("binding read", err)
	}
	os.Chmod(path, 0644)
	if _, err := readDeviceBinding(path); err == nil {
		t.Fatal("accepted readable credentials")
	}
	os.Remove(path)
	os.Symlink(filepath.Join(t.TempDir(), "target"), path)
	if err := writePrivateJSON(path, binding); err == nil {
		t.Fatal("followed symlink")
	}
}

func TestDeviceAccessLeaseAndLocalLogoutCancelStreams(t *testing.T) {
	var status int = 200
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 43) {
			t.Error("wrong device credential")
		}
		writer.WriteHeader(status)
		json.NewEncoder(writer).Encode(map[string]any{"device_id": "m101", "owner_email": "owner@example.com", "lease_until": time.Now().Add(time.Second).Unix()})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	binding := deviceBinding{Origin: server.URL, DeviceID: "m101", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43), OwnerEmail: "owner@example.com"}
	writePrivateJSON(path, binding)
	access := newDeviceAccess(path)
	defer access.close()
	access.refresh(context.Background())
	closed := make(chan struct{})
	handler := access.handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Fleet-Device-Token") != "" {
			t.Error("leaked secret into application")
		}
		writer.WriteHeader(200)
		<-request.Context().Done()
		close(closed)
	}))
	request := httptest.NewRequest("GET", "/api/chat/events", nil)
	request.Header.Set("X-Fleet-Device-ID", "m101")
	request.Header.Set("X-Fleet-Device-Token", binding.ProxyToken)
	go handler.ServeHTTP(httptest.NewRecorder(), request)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("lease expiry retained stream")
	}
	status = 403
	access.refresh(context.Background())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("disabled access %d", response.Code)
	}
	status = 200
	access.refresh(context.Background())
	binding.Locked = true
	writePrivateJSON(path, binding)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("local logout retained access")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/health", nil))
	if response.Code != 200 || response.Body.String() != "ok" {
		t.Fatal("minimal health")
	}
}

func TestLocalLogoutClosesExistingRequestWithoutWaitingForLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	binding := deviceBinding{Origin: "https://fleet.example.com", DeviceID: "m1", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43)}
	writePrivateJSON(path, binding)
	access := newDeviceAccess(path)
	access.binding = binding
	access.lease = time.Now().Add(40 * time.Second)
	access.scope, access.cancel = context.WithCancel(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go access.watchLocalBinding(ctx)
	started, closed := make(chan struct{}), make(chan struct{})
	handler := access.handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(closed)
	}))
	request := httptest.NewRequest("GET", "/api/chat/events", nil)
	request.Header.Set("X-Fleet-Device-ID", "m1")
	request.Header.Set("X-Fleet-Device-Token", binding.ProxyToken)
	go handler.ServeHTTP(httptest.NewRecorder(), request)
	<-started
	binding.Locked = true
	writePrivateJSON(path, binding)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("local logout failed to cancel existing request")
	}
}

func TestDeviceLeaseIsNeverExtendedByServiceFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(503) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	binding := deviceBinding{Origin: server.URL, DeviceID: "m1", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43)}
	writePrivateJSON(path, binding)
	access := newDeviceAccess(path)
	defer access.close()
	access.binding = binding
	access.lease = time.Now().Add(400 * time.Millisecond)
	access.scope, access.cancel = context.WithCancel(context.Background())
	access.timer = time.AfterFunc(time.Until(access.lease), access.cancel)
	lease := access.lease
	access.refresh(context.Background())
	if access.lease != lease {
		t.Fatal("transient failure changed existing lease")
	}
	request := httptest.NewRequest("GET", "/api/info", nil)
	request.Header.Set("X-Fleet-Device-ID", "m1")
	request.Header.Set("X-Fleet-Device-Token", binding.ProxyToken)
	handler := access.handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(204) }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("valid cached lease rejected")
	}
	<-access.scope.Done()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("expired offline lease allowed")
	}
}

func TestDeviceServiceRoutingUsesServerAssignedPorts(t *testing.T) {
	child := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.Write([]byte(request.URL.Path)) }))
	defer child.Close()
	address, _ := url.Parse(child.URL)
	port, _ := strconv.Atoi(address.Port())
	mux := http.NewServeMux()
	registerDeviceServices(mux, deviceBinding{DeviceID: "m101", TerminalPort: port, FilesPort: port})
	for _, path := range []string{"/m101/term/", "/m101/files/api/resources"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 || response.Body.String() != path {
			t.Fatal("wrong scoped child service routing")
		}
	}
}

func TestInstallationCompletionKeepsExistingLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	binding := deviceBinding{Origin: "https://fleet.example.com", DeviceID: "m1", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43)}
	writePrivateJSON(path, binding)
	access := newDeviceAccess(path)
	defer access.close()
	access.binding = binding
	access.lease = time.Now().Add(30 * time.Second)
	access.scope, access.cancel = context.WithCancel(context.Background())
	binding.Complete = true
	writePrivateJSON(path, binding)
	request := httptest.NewRequest("GET", "/api/info", nil)
	request.Header.Set("X-Fleet-Device-ID", "m1")
	request.Header.Set("X-Fleet-Device-Token", binding.ProxyToken)
	response := httptest.NewRecorder()
	access.handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(204) })).ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatal("completion flag unnecessarily invalidated authorization")
	}
}

func TestDeviceNameFollowsValidatedLeaseWithoutChangingAuthorization(t *testing.T) {
	name, id := "Office Mac", "m1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		json.NewEncoder(writer).Encode(map[string]any{"device_id": id, "device_name": name, "owner_email": "owner@example.test", "lease_until": time.Now().Add(40 * time.Second).Unix()})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "private", "binding.json")
	binding := deviceBinding{Origin: server.URL, DeviceID: "m1", OwnerEmail: "owner@example.test", DeviceToken: strings.Repeat("a", 43), ProxyToken: strings.Repeat("b", 43), Complete: true}
	if err := writePrivateJSON(path, binding); err != nil {
		t.Fatal(err)
	}
	access := newDeviceAccess(path)
	defer access.close()
	access.refresh(context.Background())
	current, err := readDeviceBinding(path)
	if err != nil || current.DeviceName != name || !sameDeviceAuthorization(binding, current) {
		t.Fatal("name not synchronized without changing authorization", err)
	}
	originalScope := access.scope
	name = "Renamed Mac"
	access.refresh(context.Background())
	current, _ = readDeviceBinding(path)
	if current.DeviceName != name || access.scope != originalScope || originalScope.Err() != nil {
		t.Fatal("rename interrupted an authorized stream")
	}
	// Older servers omit the name; retain the last authoritative name.
	name = ""
	access.refresh(context.Background())
	current, _ = readDeviceBinding(path)
	if current.DeviceName != "Renamed Mac" {
		t.Fatal("old server erased cached name")
	}
	name, id = "Wrong device", "m2"
	access.refresh(context.Background())
	current, _ = readDeviceBinding(path)
	if current.DeviceName != "Renamed Mac" || originalScope.Err() == nil {
		t.Fatal("unverified identity updated metadata or retained access")
	}
}
