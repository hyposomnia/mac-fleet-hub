package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDesktopMaintenanceFencesRequestsAndRestoresOnRejection(t *testing.T) {
	gate := &desktopMaintenanceGate{}
	if !gate.Enter() {
		t.Fatal("unexpected rejection")
	}
	if gate.Prepare(func() error { return nil }) == nil {
		t.Fatal("interrupted in-flight operation")
	}
	gate.Leave()
	if gate.Prepare(func() error { return errors.New("active Fleet turn") }) == nil {
		t.Fatal("ignored active turn")
	}
	if !gate.Enter() {
		t.Fatal("failed guard left service frozen")
	}
	gate.Leave()
	if err := gate.Prepare(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if gate.Enter() {
		gate.Leave()
		t.Fatal("accepted operation after update/stop was prepared")
	}
	gate.Resume()
	if !gate.Enter() {
		t.Fatal("cancelled update did not resume service")
	}
	gate.Leave()
}

func TestDesktopMaintenanceRejectsNewWebsocketDuringStop(t *testing.T) {
	gate := &desktopMaintenanceGate{}
	if err := gate.Prepare(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	defer gate.Resume()
	request := httptest.NewRequest(http.MethodGet, "/m1/term/ws", nil)
	request.Header.Set("Upgrade", "websocket")
	recorder := httptest.NewRecorder()
	gate.Handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(200) })).ServeHTTP(recorder, request)
	if recorder.Code != 503 {
		t.Fatal("allowed a new terminal while stopping")
	}
}

func TestDesktopMaintenanceOldTimeoutCannotUnlockNewOperation(t *testing.T) {
	gate := &desktopMaintenanceGate{}
	if err := gate.Prepare(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	previous := gate.generation
	gate.Resume()
	if err := gate.Prepare(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	defer gate.Resume()
	gate.resumeGeneration(previous)
	if gate.Enter() {
		gate.Leave()
		t.Fatal("expired timer unlocked another maintenance operation")
	}
}

func TestDesktopMaintenanceDetachesIdleSocketWithoutBlocking(t *testing.T) {
	gate := &desktopMaintenanceGate{}
	started := make(chan struct{})
	done := make(chan struct{})
	request := httptest.NewRequest(http.MethodGet, "/m1/term/ws", nil)
	request.Header.Set("Upgrade", "websocket")
	go func() {
		gate.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		})).ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	<-started
	if err := gate.Prepare(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	defer gate.Resume()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle socket did not detach")
	}
}

func TestDesktopMaintenanceBusyCheckPreservesSockets(t *testing.T) {
	gate := &desktopMaintenanceGate{}
	scope, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/m1/term/ws", nil).WithContext(scope)
	request.Header.Set("Upgrade", "websocket")
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		gate.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() })).ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	<-started
	if err := gate.Prepare(func() error { return errors.New("active task") }); err == nil {
		t.Fatal("ignored active task")
	}
	select {
	case <-done:
		t.Fatal("rejected maintenance disconnected terminal")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	<-done
}
