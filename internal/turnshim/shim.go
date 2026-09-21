// Package turnshim 把本机的 TCP 连接隧道到远端。
//
// 它存在的唯一理由：TURN 客户端自己不会用 HTTP 代理，而有些网络只放行代理。
// 在回环地址上开一个 TCP 监听，让 TURN 以为自己在连本机，实际流量被塞进
// 一条已经能通的 WSS 里，由控制面转发给 coturn。
package turnshim

import (
	"context"
	"errors"
	"io"
	"net"
)

// DialFunc 为每条被接受的连接建立一条隧道。
type DialFunc func(ctx context.Context) (net.Conn, error)

// Shim 在回环地址上监听，把每条连接交给 DialFunc 建立的隧道。
type Shim struct {
	listener net.Listener
	dial     DialFunc
	ctx      context.Context
}

// Start 开始监听。addr 传 "127.0.0.1:0" 时由系统分配端口，用 Addr() 读回来。
func Start(ctx context.Context, addr string, dial DialFunc) (*Shim, error) {
	if dial == nil {
		return nil, errors.New("turnshim: 缺少隧道拨号实现")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Shim{listener: listener, dial: dial, ctx: ctx}
	go s.serve()
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	return s, nil
}

// Addr 返回实际监听地址。
func (s *Shim) Addr() string { return s.listener.Addr().String() }

// Close 停止监听。已经建立的隧道随进程一起结束。
func (s *Shim) Close() error { return s.listener.Close() }

func (s *Shim) serve() {
	for {
		local, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.tunnel(local)
	}
}

// tunnel 把一条本地连接和一条隧道连接对接起来。任一方向结束就整体收摊，
// 因为 TURN/TCP 的连接语义是"断了就是断了"，留着半条只会积累僵尸连接。
func (s *Shim) tunnel(local net.Conn) {
	remote, err := s.dial(s.ctx)
	if err != nil {
		_ = local.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, remote); done <- struct{}{} }()
	<-done
	_ = local.Close()
	_ = remote.Close()
	<-done
}
