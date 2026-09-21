package hubclient

import (
	"context"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/hueshu/relaymic/internal/signaling"
)

// DialTunnel 拨一条到控制面的隧道连接，并把 WebSocket 当成 net.Conn 用。
//
// 这是给"只能走 HTTP 代理"的网络准备的：TURN 客户端不会用代理，于是把
// TURN/TCP 流量塞进这条已经能通的 WSS，由控制面转发给 coturn。
func (c *Client) DialTunnel(ctx context.Context) (net.Conn, error) {
	target, err := signaling.TunnelURL(c.url)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)
	conn, _, err := c.dial(ctx, target, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(signaling.MaxMessageBytes)
	return websocket.NetConn(ctx, conn, websocket.MessageBinary), nil
}
