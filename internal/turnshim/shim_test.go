package turnshim

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
)

func TestShimPipesBytesBothWays(t *testing.T) {
	remote, peer := net.Pipe()
	defer peer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shim, err := Start(ctx, "127.0.0.1:0", func(context.Context) (net.Conn, error) {
		return remote, nil
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer shim.Close()

	local, err := net.Dial("tcp", shim.Addr())
	if err != nil {
		t.Fatalf("dial shim: %v", err)
	}
	defer local.Close()

	if _, err := local.Write([]byte("ping")); err != nil {
		t.Fatalf("write to shim: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(peer, buf); err != nil {
		t.Fatalf("read tunneled bytes: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("tunnel delivered %q, want %q", buf, "ping")
	}

	if _, err := peer.Write([]byte("pong")); err != nil {
		t.Fatalf("write to tunnel peer: %v", err)
	}
	if _, err := io.ReadFull(local, buf); err != nil {
		t.Fatalf("read bytes back from shim: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("shim delivered %q, want %q", buf, "pong")
	}
}

func TestShimListensOnLoopbackAndStopsAfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shim, err := Start(ctx, "127.0.0.1:0", func(context.Context) (net.Conn, error) {
		return nil, io.EOF
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	addr := shim.Addr()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("Addr() = %q, want a loopback address", addr)
	}
	if err := shim.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if conn, err := net.Dial("tcp", addr); err == nil {
		conn.Close()
		t.Fatal("shim still accepted a connection after Close()")
	}
}

func TestShimDropsConnectionWhenDialFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shim, err := Start(ctx, "127.0.0.1:0", func(context.Context) (net.Conn, error) {
		return nil, io.EOF
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer shim.Close()

	local, err := net.Dial("tcp", shim.Addr())
	if err != nil {
		t.Fatalf("dial shim: %v", err)
	}
	defer local.Close()

	buf := make([]byte, 1)
	if _, err := local.Read(buf); err == nil {
		t.Fatal("read succeeded, want the shim to close a connection it could not tunnel")
	}
}
