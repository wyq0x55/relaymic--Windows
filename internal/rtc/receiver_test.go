package rtc

import (
	"net"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestIsCGNAT(t *testing.T) {
	cases := map[string]bool{
		"100.100.100.100": true,  // Tailscale
		"100.64.0.0":      true,  // 段首
		"100.127.255.255": true,  // 段尾
		"100.63.255.255":  false, // 段外
		"100.128.0.1":     false, // 段外
		"192.168.31.82":   false, // 局域网
		"8.8.8.8":         false, // 公网
		// Tailscale 的 IPv6：漏掉这段的话 ICE 会从 v6 绕过去
		"fd7a:115c:a1e0::bb38:735a": true,
		"fd7a:115c:a1e0:ab12::1":    true,
		"fd7b:115c:a1e0::1":         false, // 相邻前缀，不该误伤
		"2001:4860:4860::8888":      false, // 公网 v6
		"::1":                       false, // 环回
	}
	for ip, want := range cases {
		if got := isCGNAT(net.ParseIP(ip)); got != want {
			t.Errorf("isCGNAT(%s) = %v, 期望 %v", ip, got, want)
		}
	}
}

func TestStripCGNATCandidates(t *testing.T) {
	sdp := strings.Join([]string{
		"v=0",
		"a=candidate:1 1 udp 2130706431 192.168.31.82 51234 typ host",
		"a=candidate:2 1 udp 2130706431 100.100.100.100 51235 typ host",
		"a=candidate:4 1 udp 2130706431 fd7a:115c:a1e0::bb38 51237 typ host",
		"a=candidate:3 1 udp 1694498815 203.0.113.7 51236 typ srflx raddr 192.168.31.82 rport 51234",
		"a=mid:0",
	}, "\r\n")

	out, dropped := stripCGNATCandidates(sdp)
	if dropped != 2 {
		t.Fatalf("剔除数 = %d, 期望 2（v4 和 v6 各一条）", dropped)
	}
	if strings.Contains(out, "fd7a:115c:a1e0") {
		t.Error("Tailscale 的 IPv6 候选没有被剔除")
	}
	if strings.Contains(out, "100.100.100.100") {
		t.Error("Tailscale 候选没有被剔除")
	}
	for _, keep := range []string{"192.168.31.82 51234", "203.0.113.7", "v=0", "a=mid:0"} {
		if !strings.Contains(out, keep) {
			t.Errorf("误删了应保留的内容: %s", keep)
		}
	}
}

// TestAnswerRequestsFECAndDTX 锁住 answer 里的两个 Opus 开关。
//
// 这个测试的价值在于它验的是 answer 而不是 offer：fmtp 表达的是"接收方要求
// 发送方怎么发"，浏览器照着 answer 里的这份去配它的编码器。发送端 offer 里
// 写了什么都不算数 —— 下面造的 offer 就故意不带 usedtx，answer 里仍须有。
func TestAnswerRequestsFECAndDTX(t *testing.T) {
	r := New(nil, nil)
	r.SetICEServers(nil) // 不查 STUN，只收 host 候选：测试不该依赖外网
	defer r.Close()

	answer, err := r.Answer(senderOffer(t))
	if err != nil {
		t.Fatalf("协商失败: %v", err)
	}
	for _, want := range []string{"useinbandfec=1", "usedtx=1"} {
		if !strings.Contains(answer.SDP, want) {
			t.Errorf("answer 缺少 %s，浏览器不会照做\n%s", want, answer.SDP)
		}
	}
}

// senderOffer 造一个只推音频的 offer，模仿浏览器那一端。
func senderOffer(t *testing.T) webrtc.SessionDescription {
	t.Helper()

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("创建发送端: %v", err)
	}
	defer pc.Close()

	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: SampleRate,
		Channels:  Channels,
	}, "audio", "probe")
	if err != nil {
		t.Fatalf("创建音轨: %v", err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatalf("添加音轨: %v", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("创建 offer: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("设置本端描述: %v", err)
	}
	<-webrtc.GatheringCompletePromise(pc)

	return *pc.LocalDescription()
}

// 链路模式是给运维看的结论，不能靠候选类型猜：开着 TURN 隧道时本端看到的是
// 回环地址上的中继候选，那和"对端在公网中继上"是两件不同的事。
func TestClassifyPathNamesTheMode(t *testing.T) {
	host := webrtc.ICECandidateStats{CandidateType: webrtc.ICECandidateTypeHost, IP: "192.168.1.5", Port: 54321}
	srflx := webrtc.ICECandidateStats{CandidateType: webrtc.ICECandidateTypeSrflx, IP: "203.0.113.7", Port: 40000}
	relay := webrtc.ICECandidateStats{CandidateType: webrtc.ICECandidateTypeRelay, IP: "198.51.100.9", Port: 3478}

	cases := []struct {
		name   string
		local  webrtc.ICECandidateStats
		remote webrtc.ICECandidateStats
		tunnel bool
		want   string
	}{
		{name: "局域网直连", local: host, remote: host, want: PathDirect},
		{name: "公网打洞成功", local: srflx, remote: srflx, want: PathDirect},
		{name: "本端走中继", local: relay, remote: srflx, want: PathRelay},
		{name: "对端走中继", local: srflx, remote: relay, want: PathRelay},
		// 隧道开着但这一对候选是直连：不能因为开着隧道就把直连说成隧道。
		{name: "隧道开着却直连", local: srflx, remote: srflx, tunnel: true, want: PathDirect},
		{name: "隧道里的中继", local: relay, remote: srflx, tunnel: true, want: PathTunnel},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyPath(tc.local, tc.remote, 42, tc.tunnel)
			if got.Mode != tc.want {
				t.Fatalf("Mode = %q，want %q", got.Mode, tc.want)
			}
			if got.RTTMs != 42 {
				t.Fatalf("RTTMs = %v，want 42", got.RTTMs)
			}
			if got.LocalCandidate != "host 192.168.1.5:54321" && tc.local == host {
				t.Fatalf("LocalCandidate = %q", got.LocalCandidate)
			}
			if got.RemoteCandidate == "" {
				t.Fatal("RemoteCandidate 丢了")
			}
		})
	}
}

func TestPathTextNamesBothEnds(t *testing.T) {
	p := Path{Mode: PathTunnel, RTTMs: 123, LocalCandidate: "relay 198.51.100.9:3478", RemoteCandidate: "srflx 203.0.113.7:40000"}
	text := p.Text()
	for _, want := range []string{"relay 198.51.100.9:3478", "srflx 203.0.113.7:40000", "123ms"} {
		if !strings.Contains(text, want) {
			t.Fatalf("Text() = %q，缺 %q", text, want)
		}
	}
}

// 还没连上时没有候选对，页面得看到一句人话而不是空白。
func TestPathTextOnAnEmptyPath(t *testing.T) {
	if got := (Path{}).Text(); got != "未能取得候选对" {
		t.Fatalf("Text() = %q", got)
	}
}
