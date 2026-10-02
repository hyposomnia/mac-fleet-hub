package main

import (
	"context"
	"net/http"
	"os"
	"testing"
)

func TestDeviceIntegrationProcess(t *testing.T) {
	if os.Getenv("FLEET_INTEGRATION_PROCESS") != "1" {
		t.Skip("isolated process helper")
	}
	binding, err := readDeviceBinding(bindingPath())
	if err != nil {
		t.Fatal(err)
	}
	access := newDeviceAccess(bindingPath())
	access.refresh(context.Background())
	go access.run(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", func(writer http.ResponseWriter, request *http.Request) { writer.Write([]byte("private-device-info")) })
	registerDeviceServices(mux, binding)
	mux.HandleFunc("/api/events", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Write([]byte("data: connected\n\n"))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	})
	if err := http.ListenAndServe(os.Getenv("FLEET_INTEGRATION_LISTEN"), access.handler(mux)); err != nil {
		t.Fatal(err)
	}
}
