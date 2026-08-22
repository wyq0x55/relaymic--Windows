// stuncheck 判断本机所在网络的 NAT 类型，并列出可用的 STUN 服务器。
//
// 这是"要不要自建 TURN"的判据：
//   - 锥形 NAT：UDP 打洞多半能成，不需要中继服务器
//   - 对称型 NAT：外部端口随目标而变，打洞基本无望，必须上 TURN
//
// 判定方法是用同一个本地 socket 去问多个 STUN，比较它们看到的外部端口。
// 用不同 socket 去问是测不出来的 —— 那样端口本来就会不同。
package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/pion/stun/v3"
)

var defaultServers = []string{
	"stun.l.google.com:19302",
	"stun.cloudflare.com:3478",
	"stun.nextcloud.com:3478",
	"stun.miwifi.com:3478",
	"stun.chat.bilibili.com:3478",
}

func main() {
	timeout := flag.Duration("timeout", 4*time.Second, "单个服务器超时")
	flag.Parse()

	servers := flag.Args()
	if len(servers) == 0 {
		servers = defaultServers
	}

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		fmt.Println("创建 socket 失败:", err)
		return
	}
	defer conn.Close()
	fmt.Printf("本地端口: %s\n\n", conn.LocalAddr())

	fmt.Printf("%-32s %-24s %s\n", "STUN 服务器", "它看到的外部地址", "耗时")
	var mapped []string
	for _, s := range servers {
		start := time.Now()
		addr, err := probe(conn, s, *timeout)
		if err != nil {
			fmt.Printf("%-32s %-24s %v\n", s, "✗ 不可用", err)
			continue
		}
		fmt.Printf("%-32s %-24s %v\n", s, addr, time.Since(start).Round(time.Millisecond))
		mapped = append(mapped, addr)
	}

	fmt.Println()
	switch {
	case len(mapped) < 2:
		fmt.Println("判定: 可用的 STUN 不足 2 个，无法判定 NAT 类型")
	case allSame(mapped):
		fmt.Println("判定: 锥形 NAT —— 同一个本地端口在所有 STUN 看来都映射到同一个外部端口")
		fmt.Println("      UDP 打洞多半能成，不必自建 TURN")
	default:
		fmt.Println("判定: 对称型 NAT —— 外部端口随目标地址而变")
		fmt.Println("      打洞成功率很低，跨网络必须有 TURN 中继兜底")
	}
}

func allSame(addrs []string) bool {
	for _, a := range addrs[1:] {
		if a != addrs[0] {
			return false
		}
	}
	return true
}

func probe(conn net.PacketConn, server string, timeout time.Duration) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp4", server)
	if err != nil {
		return "", err
	}
	if _, err := conn.WriteTo(stun.MustBuild(stun.TransactionID, stun.BindingRequest).Raw, raddr); err != nil {
		return "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))

	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return "", err
		}
		// 并发响应可能穿插，只认当前这台服务器回的包。
		if from.String() != raddr.String() {
			continue
		}
		msg := &stun.Message{Raw: append([]byte{}, buf[:n]...)}
		if err := msg.Decode(); err != nil {
			return "", err
		}
		var xor stun.XORMappedAddress
		if err := xor.GetFrom(msg); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s:%d", xor.IP, xor.Port), nil
	}
}
