package multiuser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeviceTransportOnlyAcceptsPrivateLoopbackProxy(t *testing.T) {
	for _, address := range []string{"http://example.com:8080", "https://127.0.0.1:8080", "http://user:password@127.0.0.1:8080", "http://127.0.0.1:8080/path", "http://127.0.0.1:8080?x=y"} {
		if _, err := NewDeviceTransport(address); err == nil {
			t.Fatalf("accepted unsafe device proxy %s", address)
		}
	}
	transport, err := NewDeviceTransport("")
	if err != nil || transport.Proxy != nil {
		t.Fatal("default must continue using direct mesh", err)
	}
}

func TestDeviceTransportUsesDedicatedMeshProxy(t *testing.T) {
	called := false
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		called = true
		if request.URL.Host != "100.96.1.9:7682" || request.URL.Path != "/api/health" {
			t.Errorf("unexpected mesh target %s", request.URL)
		}
		writer.Write([]byte("ok"))
	}))
	defer proxy.Close()
	transport, err := NewDeviceTransport(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Get("http://100.96.1.9:7682/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if !called || string(body) != "ok" {
		t.Fatal("dedicated mesh proxy was not used")
	}
}

func TestDeviceTransportAllowsColdResponseWithoutCuttingStream(t *testing.T) {
	if testing.Short() {
		t.Skip("cold-start response regression takes 16 seconds")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(16 * time.Second)
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		writer.Write([]byte("ready"))
	}))
	defer upstream.Close()
	transport, err := NewDeviceTransport("")
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport}).Get(upstream.URL + "/api/chat/start")
	if err != nil {
		t.Fatalf("cold start rejected before response: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "ready" {
		t.Fatalf("response=%s error=%v", body, err)
	}
}
