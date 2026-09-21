// signaling 是 RelayMic 的公网控制面。
//
// 它是整条链路上唯一需要公网可达的组件：监听 443，一边等浏览器连进来、
// 一边等接收端主动连进来，用 6 位配对码把两边撮合到一起，然后原样转发 SDP。
// 音频永远不经过这里 —— 两端之间直连，打不通时走 TURN（#3）。
//
// 接收端因此不再需要公网入站，也不需要 Tailscale：它只是又一个出站 HTTPS 客户端。
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/hueshu/relaymic/internal/signaling"
	"github.com/hueshu/relaymic/internal/tlscert"
	"github.com/hueshu/relaymic/internal/web"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（JSON）；用 -gen-token 时不需要")
	addr := flag.String("addr", "", "覆盖监听地址，默认取配置文件里的 listen")
	genToken := flag.Bool("gen-token", false, "生成一个接收端 token 并退出")
	genReceiver := flag.String("gen-receiver", "", "生成一段接收端配置（名字用这个值）并退出")
	selfSignedDir := flag.String("self-signed-dir", "", "本地自测：在该目录生成自签证书并直接启用 TLS")
	plain := flag.Bool("plain", false, "本地自测：用 http 而非 https；浏览器只在 localhost 上才给麦克风权限")
	flag.Parse()

	log.SetFlags(log.Ltime)
	log.Println("RelayMic signaling  Copyright (C) 2026 Shu Chunhui")
	log.Println("本程序不提供任何担保，遵循 AGPL-3.0 发布。")
	log.Println("源码：https://github.com/hueshu/relaymic")

	if *genToken || *genReceiver != "" {
		token, err := signaling.NewToken()
		if err != nil {
			log.Fatalln("生成 token 失败:", err)
		}
		if *genReceiver != "" {
			fmt.Printf("  {\"name\": %q, \"token\": %q}\n", *genReceiver, token)
			return
		}
		fmt.Println(token)
		return
	}

	if *configPath == "" {
		log.Fatalln("缺少 -config：先写一份配置文件，或用 -gen-token 生成接收端凭据")
	}
	cfg, err := signaling.LoadFile(*configPath)
	if err != nil {
		log.Fatalln(err)
	}
	registry, err := cfg.Registry()
	if err != nil {
		log.Fatalln("配置里的接收端不可用:", err)
	}
	turnIssuer, err := cfg.TurnIssuer()
	if err != nil {
		log.Fatalln("配置里的 TURN 不可用:", err)
	}
	server, err := signaling.NewServer(signaling.ServerConfig{
		Registry:       registry,
		ICEServers:     cfg.ICEServers,
		TurnIssuer:     turnIssuer,
		Page:           web.SenderHTML,
		AllowedOrigins: cfg.AllowedOrigins,
	})
	if err != nil {
		log.Fatalln(err)
	}

	listen := cfg.Listen
	if *addr != "" {
		listen = *addr
	}
	httpSrv := &http.Server{Addr: listen, Handler: server.Handler()}

	if !*plain {
		cert, err := loadCert(cfg, *selfSignedDir)
		if err != nil {
			log.Fatalln("准备证书失败:", err)
		}
		// 浏览器只在安全上下文里给麦克风权限，所以线上必须走 TLS；
		// 1.2 是下限，再低就不该拿来跑公网信令。
		httpSrv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	go func() {
		scheme := "https"
		if *plain {
			scheme = "http"
		}
		log.Printf("控制面监听 %s（%s）", listen, scheme)
		log.Printf("发送端地址: %s://%s", scheme, hostForDisplay(listen))
		log.Printf("接收端连到: %s://%s/ws/receiver", "wss", hostForDisplay(listen))
		if len(cfg.AllowedOrigins) > 0 {
			log.Printf("允许的来源: %v", cfg.AllowedOrigins)
		} else {
			log.Println("允许的来源: 同源（未配置 allowedOrigins）")
		}
		log.Printf("已配置接收端: %d 台", len(cfg.Receivers))
		if turnIssuer != nil {
			log.Println("TURN: 配对后签发短期凭据")
		}

		var err error
		if *plain {
			err = httpSrv.ListenAndServe()
		} else {
			err = httpSrv.ListenAndServeTLS("", "")
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalln(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("退出")
	_ = httpSrv.Close()
}

// loadCert 优先用配置里的证书；本地自测时退到自签证书。
// 两者都没有就直接失败 —— 静默降级成明文会让"线上跑起来了"变成假象。
func loadCert(cfg *signaling.FileConfig, selfSignedDir string) (tls.Certificate, error) {
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		return tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	}
	if selfSignedDir != "" {
		hosts := append([]string{"localhost", "127.0.0.1"}, localIPv4()...)
		return tlscert.Ensure(selfSignedDir, hosts)
	}
	return tls.Certificate{}, signaling.ErrNoTLS
}

// hostForDisplay 把监听地址变成"用户该输进浏览器的那个名字"。
func hostForDisplay(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<这台机器的域名或 IP>"
	}
	if port == "443" {
		return host
	}
	return net.JoinHostPort(host, port)
}

// localIPv4 列出本机对外可达的 IPv4，用来写进自签证书的 SAN。
func localIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	return ips
}
