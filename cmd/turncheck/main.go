// turncheck 向 TURN 服务器申请一次中继分配，并验证中继地址真的能收发数据。
//
// STUN 通了不代表 TURN 能用：分配请求走 3478，实际中继却走另一段端口，
// 那段端口没放行的话，ICE 会拿到 relay 候选却永远连不通。
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/pion/turn/v5"
)

func main() {
	server := flag.String("server", "", "TURN 服务器 host:port")
	user := flag.String("user", "", "用户名")
	pass := flag.String("pass", "", "密码")
	realm := flag.String("realm", "", "realm")
	flag.Parse()

	if *server == "" || *user == "" {
		fmt.Fprintln(os.Stderr, "用法: turncheck -server host:3478 -user U -pass P -realm R")
		os.Exit(2)
	}

	conn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		die("创建本地 socket", err)
	}
	defer conn.Close()

	client, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: *server,
		TURNServerAddr: *server,
		Conn:           conn,
		Username:       *user,
		Password:       *pass,
		Realm:          *realm,
	})
	if err != nil {
		die("创建 TURN 客户端", err)
	}
	defer client.Close()

	if err := client.Listen(); err != nil {
		die("启动监听", err)
	}

	start := time.Now()
	mapped, err := client.SendBindingRequest()
	if err != nil {
		die("STUN 绑定请求", err)
	}
	fmt.Printf("STUN 绑定   ✓  看到的外部地址 %s  (%v)\n", mapped, time.Since(start).Round(time.Millisecond))

	start = time.Now()
	relay, err := client.Allocate()
	if err != nil {
		die("TURN 分配（认证失败或中继端口段未放行）", err)
	}
	defer relay.Close()
	fmt.Printf("TURN 分配   ✓  中继地址 %s  (%v)\n", relay.LocalAddr(), time.Since(start).Round(time.Millisecond))

	// 真正的验证：再要一个中继地址，让两个中继互发。
	// 两端都是服务器的公网地址，不会被 denied-peer-ip 规则挡下 ——
	// 用本机内网地址做对端测不出结果，那是配置在正确工作。
	peerConn, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		die("创建第二个 socket", err)
	}
	defer peerConn.Close()

	peerClient, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: *server,
		TURNServerAddr: *server,
		Conn:           peerConn,
		Username:       *user,
		Password:       *pass,
		Realm:          *realm,
	})
	if err != nil {
		die("创建第二个 TURN 客户端", err)
	}
	defer peerClient.Close()
	if err := peerClient.Listen(); err != nil {
		die("第二个客户端监听", err)
	}
	peerRelay, err := peerClient.Allocate()
	if err != nil {
		die("第二次 TURN 分配", err)
	}
	defer peerRelay.Close()
	fmt.Printf("第二个中继 ✓  %s\n", peerRelay.LocalAddr())

	payload := []byte("relaymic-turn-probe")
	start = time.Now()
	if _, err := peerRelay.WriteTo(payload, relay.LocalAddr()); err != nil {
		die("经中继发包", err)
	}

	buf := make([]byte, 1500)
	_ = relay.SetReadDeadline(time.Now().Add(6 * time.Second))
	n, from, err := relay.ReadFrom(buf)
	if err != nil {
		fmt.Printf("中继转发   ✗  分配成功但数据不通：%v\n", err)
		os.Exit(1)
	}
	if string(buf[:n]) != string(payload) {
		fmt.Printf("中继转发   ✗  收到的数据不一致\n")
		os.Exit(1)
	}
	fmt.Printf("中继转发   ✓  %d 字节原样送达，来自 %s  (%v)\n",
		n, from, time.Since(start).Round(time.Millisecond))
	fmt.Println("\n结论: TURN 完全可用，音频可以经它中继")
}

func die(what string, err error) {
	fmt.Fprintf(os.Stderr, "%s 失败: %v\n", what, err)
	os.Exit(1)
}
