package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexDesktopReadStateUsesNativeTool(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(map[bool]string{false: "unread", true: "read"}[read], func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "fleet-read-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "app.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				for id := 1; id <= 2; id++ {
					var header [4]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						t.Error(err)
						return
					}
					data := make([]byte, binary.LittleEndian.Uint32(header[:]))
					if _, err := io.ReadFull(conn, data); err != nil {
						t.Error(err)
						return
					}
					var request struct {
						ID     int            `json:"id"`
						Method string         `json:"method"`
						Params map[string]any `json:"params"`
					}
					if err := json.Unmarshal(data, &request); err != nil {
						t.Error(err)
						return
					}
					var result any = map[string]any{"tools": []map[string]string{{"name": "set_thread_read_state", "namespace": "codex_app"}}}
					if id == 1 && request.Method != "tools/list" {
						t.Errorf("missing capability discovery %+v", request)
					}
					if id == 2 {
						args, _ := request.Params["arguments"].(map[string]any)
						if request.Method != "tools/call" || request.Params["tool"] != "set_thread_read_state" || request.Params["namespace"] != "codex_app" || args["read"] != read || args["threadId"] != "fixture-thread" || args["hostId"] != "local" || request.Params["threadId"] != "fixture-thread" {
							t.Errorf("wrong native mutation: %+v", request)
						}
						result = map[string]any{"success": true, "contentItems": []any{}}
					}
					data, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
					binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
					conn.Write(header[:])
					conn.Write(data)
				}
			}()
			if err := callCodexDesktopReadState(context.Background(), path, "fixture-thread", read); err != nil {
				t.Fatal(err)
			}
			<-done
		})
	}
}

func TestCodexDesktopReadStateRejectsMissingToolAndBadResponse(t *testing.T) {
	for _, kind := range []string{"missing", "oversized", "error"} {
		t.Run(kind, func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "fleet-read-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "app.sock")
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				var header [4]byte
				io.ReadFull(conn, header[:])
				io.CopyN(io.Discard, conn, int64(binary.LittleEndian.Uint32(header[:])))
				data := []byte(`{"id":1,"jsonrpc":"2.0","result":{"tools":[]}}`)
				if kind == "error" {
					data = []byte(`{"id":1,"jsonrpc":"2.0","error":{"code":-1,"message":"unavailable"}}`)
				}
				binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
				if kind == "oversized" {
					binary.LittleEndian.PutUint32(header[:], 8<<20+1)
				}
				conn.Write(header[:])
				conn.Write(data)
			}()
			if err := callCodexDesktopReadState(context.Background(), path, "fixture-thread", false); err == nil {
				t.Fatal("invalid Desktop response accepted")
			}
		})
	}
}
