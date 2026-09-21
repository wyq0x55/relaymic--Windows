package signaling

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// echoServer 冒充 coturn 的 TCP 监听：收到什么就回什么。
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(conn, conn)
				_ = conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

func (h *testHub) dialTunnel(t *testing.T, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	return websocket.Dial(ctx, h.wsURL("/ws/tunnel"), &websocket.DialOptions{HTTPHeader: header})
}

func TestTunnelIsDisabledWithoutConfiguredTarget(t *testing.T) {
	hub := newTestHub(t)
	conn, resp, err := hub.dialTunnel(t, hub.token)
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("tunnel accepted a connection while no target is configured")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %v, want 404", resp)
	}
}

func TestTunnelRequiresReceiverToken(t *testing.T) {
	hub := newTestHub(t)
	hub.server.tunnelTarget = echoServer(t)
	for _, token := range []string{"", "wrong-token"} {
		conn, resp, err := hub.dialTunnel(t, token)
		if err == nil {
			_ = conn.CloseNow()
			t.Fatalf("tunnel accepted token %q", token)
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %v, want 401", resp)
		}
	}
}

func TestTunnelPipesBytesToConfiguredTarget(t *testing.T) {
	hub := newTestHub(t)
	hub.server.tunnelTarget = echoServer(t)

	conn, _, err := hub.dialTunnel(t, hub.token)
	if err != nil {
		t.Fatalf("dialTunnel() error = %v", err)
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)

	if _, err := stream.Write([]byte("hello")); err != nil {
		t.Fatalf("write into tunnel: %v", err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read from tunnel: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("tunnel returned %q, want %q", buf, "hello")
	}
}
