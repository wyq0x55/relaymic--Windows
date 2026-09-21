package signaling

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/coder/websocket"
)

// TunnelPath 是接收端把 TURN 流量隧道到控制面的端点。
//
// 它只对通过 token 认证的接收端开放，且只能转发到配置里写死的那个地址 ——
// 一个能连任意目标的中继就是一个开放的代理。
const TunnelPath = "/ws/tunnel"

// TunnelURL 把接收端连的控制面地址换成同一台 Hub 上的隧道端点。
func TunnelURL(hubURL string) (string, error) {
	u, err := url.Parse(hubURL)
	if err != nil {
		return "", fmt.Errorf("解析控制面地址: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return "", fmt.Errorf("控制面地址必须是 ws:// 或 wss://，收到 %q", hubURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("控制面地址缺少主机: %q", hubURL)
	}
	u.Path = TunnelPath
	u.RawQuery = ""
	return u.String(), nil
}

// handleTunnel 把一条 WebSocket 连接对接给本机的 TURN 服务。
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	if s.tunnelTarget == "" {
		// 没配置就是没这个功能，不要让人以为握手失败是网络问题。
		http.NotFound(w, r)
		return
	}
	if _, ok := s.registry.Authenticate(bearerToken(r)); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(MaxMessageBytes)
	defer conn.CloseNow()

	target, err := net.Dial("tcp", s.tunnelTarget)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "tunnel target")
		return
	}
	defer target.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream := websocket.NetConn(ctx, conn, websocket.MessageBinary)

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(stream, target); done <- struct{}{} }()
	go func() { _, _ = io.Copy(target, stream); done <- struct{}{} }()
	<-done
	_ = stream.Close()
	_ = target.Close()
	<-done
}
