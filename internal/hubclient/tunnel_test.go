package hubclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hueshu/relaymic/internal/signaling"
)

func TestDialTunnelUsesTunnelEndpointAndToken(t *testing.T) {
	var gotPath, gotAuth string
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		stream := websocket.NetConn(r.Context(), conn, websocket.MessageBinary)
		_, _ = io.Copy(stream, stream)
	}))
	defer echo.Close()

	client, err := New(Config{
		URL:   "ws" + strings.TrimPrefix(echo.URL, "http") + "/ws/receiver",
		Token: "tok",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.DialTunnel(ctx)
	if err != nil {
		t.Fatalf("DialTunnel() error = %v", err)
	}
	defer stream.Close()

	if _, err := stream.Write([]byte("ping")); err != nil {
		t.Fatalf("write into tunnel: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(stream, buf); err != nil {
		t.Fatalf("read from tunnel: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("tunnel returned %q, want %q", buf, "ping")
	}
	if gotPath != signaling.TunnelPath {
		t.Fatalf("tunnel path = %q, want %q", gotPath, signaling.TunnelPath)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q, want the receiver token", gotAuth)
	}
}

func TestDialTunnelRejectsNonWebSocketHubURL(t *testing.T) {
	client, err := New(Config{URL: "https://mic.example.com/ws/receiver", Token: "tok"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.DialTunnel(context.Background()); err == nil {
		t.Fatal("DialTunnel() accepted an https:// hub URL")
	}
}
