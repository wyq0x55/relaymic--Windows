package discover

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// 自动发现：向 Tailscale 要设备清单，逐个探测接收端口。
//
// 新 Mac 跑完部署脚本就自动出现在发送端里，不需要任何人去
// 界面上填 IP —— 机器的增减本来就不该是用户的心智负担。

// tailscaleBin 找 tailscale 命令行。装了 Tailscale 的机器基本都有，
// 但不一定在 PATH 里。
func tailscaleBin() string {
	candidates := []string{}
	if p, err := exec.LookPath("tailscale"); err == nil {
		candidates = append(candidates, p)
	}
	candidates = append(candidates,
		"/usr/local/bin/tailscale",
		`C:\Program Files\Tailscale\tailscale.exe`,
		// GUI 二进制放最后：它 --version 能跑，但 status 输出的不是
		// JSON —— launchd 的 PATH 里没有 /usr/local/bin 时曾被误选中。
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
	)
	for _, c := range candidates {
		// 用真实命令验证：能跑且输出确实是 JSON 才算数。
		probe := exec.Command(c, "status", "--json")
		hideWindow(probe)
		out, err := probe.Output()
		if err == nil && len(out) > 0 && out[0] == '{' {
			return c
		}
	}
	return ""
}

// SelfName 返回本机在 Tailscale 里的设备名（管理台里设定的那个）。
// 拿不到返回空串，调用方自己找退路。
//
// 优先走 MagicDNS 反查而不是 tailscale 命令行：App Store 版的二进制
// 在 launchd 的精简环境里起不来（"The Tailscale GUI failed to start"），
// 而 DNS 反查是纯网络调用，什么环境都一样。
func SelfName() string {
	if n := selfNameFromDNS(); n != "" {
		return n
	}
	return selfNameFromCLI()
}

// selfNameFromDNS 从网卡上找到自己的 Tailscale IP（100.64/10 段），
// 用 MagicDNS 的 PTR 反查出设备名。
func selfNameFromDNS() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipn.IP.To4()
		if v4 == nil || v4[0] != 100 || v4[1] < 64 || v4[1] > 127 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		names, err := net.DefaultResolver.LookupAddr(ctx, v4.String())
		cancel()
		if err != nil || len(names) == 0 {
			return ""
		}
		// "huemac-mini-2.tailxxxx.ts.net." → 第一段
		name := names[0]
		for i := 0; i < len(name); i++ {
			if name[i] == '.' {
				return name[:i]
			}
		}
		return name
	}
	return ""
}

func selfNameFromCLI() string {
	bin := tailscaleBin()
	if bin == "" {
		return ""
	}
	cmd := exec.Command(bin, "status", "--json")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	var st struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if json.Unmarshal(out, &st) != nil {
		return ""
	}
	// DNSName 形如 "huemac-mini1.tailxxxx.ts.net."，第一段就是设备名。
	name := st.Self.DNSName
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			return name[:i]
		}
	}
	return name
}

// tailnetPeers 返回 tailnet 内所有在线设备的 IPv4。
func tailnetPeers() ([]string, error) {
	bin := tailscaleBin()
	if bin == "" {
		return nil, fmt.Errorf("找不到 tailscale 命令")
	}
	cmd := exec.Command(bin, "status", "--json")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}
	var st struct {
		Peer map[string]struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
			Online       bool     `json:"Online"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	ips := []string{}
	for _, p := range st.Peer {
		if !p.Online {
			continue
		}
		for _, ip := range p.TailscaleIPs {
			// 只要 IPv4（100.x.y.z）
			if len(ip) > 0 && ip[0] == '1' && !contains(ip, ':') {
				ips = append(ips, ip)
				break
			}
		}
	}
	return ips, nil
}

func contains(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// probeReceiver 探测一个 IP 是不是接收端：/ice-config 响应即是。
// 超时要短：扫的是整个 tailnet，大多数设备根本没开 7420。
func probeReceiver(ip string) bool {
	c := &http.Client{
		Timeout: 1500 * time.Millisecond,
		// 接收端都是自签证书，这里只判存活
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := c.Get("https://" + ip + ":7420/ice-config")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// DiscoverReceivers 扫一轮 tailnet，返回所有接收端地址。并发探测，
// 一轮耗时 ≈ 单个超时（1.5s），不随设备数线性增长。
func Receivers() ([]string, error) {
	ips, err := tailnetPeers()
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	found := []string{}
	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			if probeReceiver(ip) {
				mu.Lock()
				found = append(found, "https://"+ip+":7420")
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	return found, nil
}
