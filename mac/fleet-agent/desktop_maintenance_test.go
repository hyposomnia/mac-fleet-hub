package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
