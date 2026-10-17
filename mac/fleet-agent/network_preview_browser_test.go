//go:build preview_uat

package main

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
	"strings"
	"testing"
)

// This harness runs the production preview handler and real browser code. Only
// host DNS/routing maps the unreachable design.internal name to a test server.
func TestNetworkPreviewBrowserServer(t *testing.T) {
	cfg.MacIndex = "1"
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `import {label} from '/module.js';document.querySelector('#module').textContent=label;
fetch('/api/info').then(r=>r.text()).then(t=>document.querySelector('#api').textContent=t).catch(e=>document.querySelector('#api').textContent=e);
const ws=new WebSocket('ws://design.internal:8788/socket');ws.onopen=()=>ws.send('WebSocket 已连接');ws.onmessage=e=>document.querySelector('#ws').textContent=e.data;
const sse=new EventSource('/events');sse.onmessage=e=>{document.querySelector('#sse').textContent=e.data;sse.close()};
try{parent.document.body;document.querySelector('#isolation').textContent='隔离失败'}catch{document.querySelector('#isolation').textContent='账号页面已隔离'}
document.querySelector('#send').onclick=()=>fetch('/api/write',{method:'POST',body:'from preview'}).then(r=>r.text()).then(t=>document.querySelector('#write').textContent=t);`)
		case "/module.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `export const label='ES Module 已加载';`)
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, `body{font:16px system-ui;background:#f2f7f5;color:#202923;padding:24px}h1{color:#376f5f}li{margin:14px 0}button{padding:12px 20px;border:0;border-radius:12px;background:#59b196}`)
		case "/api/info":
			fmt.Fprint(w, "宿主网络 API 已连接")
		case "/api/write":
			fmt.Fprint(w, "POST 已送达宿主服务")
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: SSE 已连接\n\n")
			w.(http.Flusher).Flush()
		case "/socket":
			up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			conn, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			kind, data, err := conn.ReadMessage()
			if err == nil {
				conn.WriteMessage(kind, data)
			}
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="/style.css"></head><body><h1>宿主网络预览</h1><p>目标：design.internal:8788（浏览器无法直接解析）</p><ul><li id="module">正在加载模块</li><li id="api">正在连接 API</li><li id="ws">正在连接 WebSocket</li><li id="sse">正在连接 SSE</li><li id="isolation">正在检查隔离</li></ul><button id="send">测试 POST</button><p id="write"></p><script type="module" src="/app.js"></script></body></html>`)
		}
	})
	go http.ListenAndServe("127.0.0.1:7199", app)
	p := newNetworkPreviewProxy().(*networkPreviewProxy)
	p.transport.Proxy = nil
	p.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if !strings.HasPrefix(address, "design.internal:") {
			return nil, fmt.Errorf("unexpected fixture host")
		}
		return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:7199")
	}
	t.Log("host preview fixture agent listening on 127.0.0.1:7198")
	t.Fatal(http.ListenAndServe("127.0.0.1:7198", p))
}
