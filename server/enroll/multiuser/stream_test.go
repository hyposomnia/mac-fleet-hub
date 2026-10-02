package multiuser

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxySSECancelsOnLogout(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "stream@example.com")
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: ready\n\n")
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(closed)
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'42','100.64.0.42','Mac','active',?)", user.ID, now.Unix())
	if _, _, err := server.issueDeviceCredentials(1); err != nil {
		t.Fatal(err)
	}
	listener := httptest.NewServer(server)
	defer listener.Close()
	request, _ := http.NewRequest("GET", listener.URL+"/m1/api/chat/events", nil)
	request.AddCookie(browser.cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: ready\n" {
		t.Fatalf("not streaming %q %v", line, err)
	}
	if result := browser.request("POST", "/api/auth/logout", nil); result.Code != 200 {
		t.Fatal(result.Body.String())
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("logout did not close SSE")
	}
}

func TestProxyWebSocketCancelsOnDeviceRevoke(t *testing.T) {
	server, now := newTestServer(t)
	browser, user, _, _ := registerBrowser(t, server, *now, "socket@example.com")
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, buffer, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		fmt.Fprint(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		buffer.Flush()
		io.Copy(io.Discard, connection)
		close(closed)
	}))
	defer upstream.Close()
	server.options.ProxyURL = func(Device, string) string { return upstream.URL }
	server.db.Exec("INSERT INTO devices(device_index,user_id,node_id,ip,name,status,created_at) VALUES(1,?,'42','100.64.0.42','Mac','active',?)", user.ID, now.Unix())
	if _, _, err := server.issueDeviceCredentials(1); err != nil {
		t.Fatal(err)
	}
	listener := httptest.NewServer(server)
	defer listener.Close()
	connection, err := net.DialTimeout("tcp", strings.TrimPrefix(listener.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(connection, "GET /m1/term/ws HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s=%s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n", strings.TrimPrefix(listener.URL, "http://"), server.options.Origin, browser.cookie.Name, browser.cookie.Value)
	buffer := bufio.NewReader(connection)
	status, err := buffer.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		t.Fatalf("websocket upgrade %q %v", status, err)
	}
	if result := browser.request("DELETE", "/api/devices/m1", nil); result.Code != 200 {
		t.Fatal(result.Body.String())
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("revocation did not close websocket")
	}
}
