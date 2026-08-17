// Package rtc 负责 WebRTC 侧：接受浏览器的 offer，收 Opus，解成 PCM。
package rtc

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

const (
	// WebRTC 的 Opus 恒定跑在 48kHz。
	SampleRate = 48000
	// SDP 里的 Opus 恒定声明为 opus/48000/2，跟实际传单声道还是立体声无关 ——
	// 真实声道数编在 Opus 包内部。这里按协商值解码：发来的若是单声道包，
	// libopus 会自己复制成左右两路，正好对上 BlackHole 2ch。
	Channels = 2
	// Opus 单帧最长 120ms，按最坏情况给解码缓冲。
	maxFrameSamples = SampleRate / 1000 * 120
)

// Receiver 持有一条到发送端的连接。
//
// 每次收到新的 offer 就重建连接 —— 发送端刷新页面、换机器、断线重来，
// 都走同一条路径，不需要额外的重连逻辑。
type Receiver struct {
	onPCM   func([]int16)
	onState func(webrtc.PeerConnectionState)
	onPath  func(string)

	stats RTPStats

	api          *webrtc.API
	dtx          bool
	iceServers   []webrtc.ICEServer
	excludeCGNAT bool
	forceRelay   bool

	mu sync.Mutex
	pc *webrtc.PeerConnection
}

// New 创建接收器。onPCM 会被解码协程反复调用，传入 48kHz 交错立体声 PCM。
func New(onPCM func([]int16), onState func(webrtc.PeerConnectionState)) *Receiver {
	return &Receiver{
		onPCM:   onPCM,
		onState: onState,
		dtx:     true,
		iceServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
}

// SetDTX 决定要不要让发送端在静音时停发包。
func (r *Receiver) SetDTX(on bool) { r.dtx = on }

// SetICEServers 覆盖默认的 STUN/TURN 配置。
func (r *Receiver) SetICEServers(servers []webrtc.ICEServer) { r.iceServers = servers }

// ForceRelay 让 ICE 只使用 TURN 中继候选。
// 直连候选一律不参与，用来确认中继链路本身是通的。
func (r *Receiver) ForceRelay(on bool) { r.forceRelay = on }

// ExcludeCGNAT 决定要不要把 100.64.0.0/10 排除在候选之外。
//
// 那个网段是 CGNAT 保留段，Tailscale 之类的覆盖网络就建在上面。
// 它对 ICE 伪装成"本地地址"、优先级高于公网反射地址，于是会被优先选中；
// 但那条路可能落到对方的中继节点上，绕上半个地球。排除它，
// 才能逼 ICE 去走真正的公网直连。
//
// 代价是：一旦公网打洞失败，又没有 TURN 兜底，就彻底连不上。
func (r *Receiver) ExcludeCGNAT(on bool) {
	r.excludeCGNAT = on
	r.api = nil // 下次协商时按新设置重建
}

// 覆盖网地址段。这些地址看着像"本地直连"，ICE 会给它们很高的优先级，
// 实际却可能落到对方的中继节点上绕半个地球。
//
// 两个段都要挡：只挡 IPv4 的话，ICE 会从 IPv6 那条路溜过去 ——
// 实测就撞见过 fd7a:... 的候选被选中，RTT 1100ms。
var overlayNets = []*net.IPNet{
	// RFC 6598 运营商级 NAT，Tailscale 的 IPv4 建在这上面
	{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)},
	// Tailscale 的 IPv6 ULA 前缀 fd7a:115c:a1e0::/48
	{IP: net.IP{0xfd, 0x7a, 0x11, 0x5c, 0xa1, 0xe0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Mask: net.CIDRMask(48, 128)},
}

func isCGNAT(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		return overlayNets[0].Contains(v4)
	}
	for _, n := range overlayNets[1:] {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// stripCGNATCandidates 删掉对端 SDP 里 CGNAT 段的候选。
//
// 只在本端过滤是不够的：浏览器那头照样会宣告它自己的覆盖网地址，
// 我们不主动剔除就仍会去尝试那条路。
func stripCGNATCandidates(sdp string) (string, int) {
	lines := strings.Split(sdp, "\r\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "a=candidate:") {
			// a=candidate:<foundation> <component> <transport> <priority> <ip> <port> typ ...
			if f := strings.Fields(line); len(f) > 4 {
				if ip := net.ParseIP(f[4]); ip != nil && isCGNAT(ip) {
					dropped++
					continue
				}
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\r\n"), dropped
}

// buildAPI 按当前设置组装 webrtc.API。用了 SettingEngine 就必须自己
// 注册编解码器和拦截器，默认的那套不会自动带上。
func (r *Receiver) buildAPI() (*webrtc.API, error) {
	if r.api != nil {
		return r.api, nil
	}
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("注册编解码器: %w", err)
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, fmt.Errorf("注册拦截器: %w", err)
	}
	se := webrtc.SettingEngine{}
	// 没有独立信令通道，answer 必须等候选收集完才能交回去（见 Answer）——
	// 于是 STUN 收集的等待时间会原封不动地变成发送端看到的白屏时间。
	// pion 默认等 5 秒，STUN 不可达时每次连接都要等满。2 秒还没回来的
	// STUN，基本就是不通，别再拖着整条协商陪葬。
	se.SetSTUNGatherTimeout(2 * time.Second)
	if r.excludeCGNAT {
		se.SetIPFilter(func(ip net.IP) bool { return !isCGNAT(ip) })
	}
	r.api = webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(ir),
		webrtc.WithSettingEngine(se),
	)
	return r.api, nil
}

// opusParams 组装 answer 里向发送端声明的 Opus 开关。
//
//	useinbandfec=1  丢包时把前一帧的低码率副本捎在下一个包里。语音丢一个包
//	                就是丢一个字，而重传要等一个 RTT —— 等到了也过了该播的时刻。
//	usedtx=1        静音时停止发包。省带宽是次要的，主要是没人说话时不再往
//	                BlackHole 灌底噪，识别引擎的静音判断更干净。
//	                可关：若编码器把轻声误判成静音，字头字尾会被掐掉，
//	                用 -dtx=false 对比是唯一可靠的排查手段。
//
// 必须写在 answer 里：fmtp 的语义是"接收方要求发送方怎么发"，浏览器照着
// 收到的 answer 去配它的编码器。我们在 offer 里看到的那份是浏览器的自我声明，
// 改它没有意义。
//
// 而 pion 生成 answer 时是把 offer 里的 fmtp 原样抄回去的（它只替换 PayloadType
// 和 rtcp-fb），所以本地 MediaEngine 注册什么 fmtp 都不影响 answer —— 想加参数
// 只能在这里落到最终 SDP 上。
func (r *Receiver) opusParams() []string {
	// maxaveragebitrate: Chrome 对语音默认只给 ~32kbps，编码噪声会直接
	// 变成识别错误。这条链路直连 RTT 18ms，96kbps 毫无压力，清晰度优先。
	params := []string{"useinbandfec=1", "maxaveragebitrate=96000"}
	if r.dtx {
		params = append(params, "usedtx=1")
	}
	return params
}

// withOpusParams 把 params 补进 answer 的 Opus fmtp 行。
// 已经声明过的参数不重复添加，避免出现互相矛盾的重复键。
func withOpusParams(sdp string, params []string) string {
	lines := strings.Split(sdp, "\r\n")

	// Opus 的 payload type 由发送端决定，不能写死 111。
	pt := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "a=rtpmap:") && strings.Contains(strings.ToLower(l), " opus/") {
			pt = strings.TrimPrefix(strings.SplitN(l, " ", 2)[0], "a=rtpmap:")
			break
		}
	}
	if pt == "" {
		return sdp
	}

	prefix := "a=fmtp:" + pt + " "
	for i, l := range lines {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		for _, p := range params {
			key := p[:strings.Index(p, "=")+1]
			if !strings.Contains(l, key) {
				l += ";" + p
			}
		}
		lines[i] = l

		return strings.Join(lines, "\r\n")
	}

	return sdp
}

// RTPStats 是 RTP 层的到达质量。
//
// 抖动缓冲该做多复杂，取决于这几个数字：包基本不丢不乱，简单的固定缓冲就够；
// 乱序严重才需要重排序，丢包严重才需要补偿。没有这些数据就调缓冲，是在猜。
type RTPStats struct {
	mu       sync.Mutex
	Received int
	Lost     int // 序号跳跃推断的丢失数
	Reorder  int // 迟到包：序号比已见到的最大值还小
	Dup      int
}

func (s *RTPStats) observe(seq uint16, first bool, maxSeq uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Received++
	if first {
		return
	}
	switch diff := int16(seq - maxSeq); {
	case diff == 1: // 顺序到达
	case diff > 1:
		s.Lost += int(diff) - 1
	case diff == 0:
		s.Dup++
	default:
		s.Reorder++
		if s.Lost > 0 {
			s.Lost-- // 之前算作丢失的包其实只是迟到
		}
	}
}

// Snapshot 取一份计数快照。
func (s *RTPStats) Snapshot() (received, lost, reorder, dup int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Received, s.Lost, s.Reorder, s.Dup
}

// Stats 返回 RTP 到达质量。
func (r *Receiver) Stats() *RTPStats { return &r.stats }

// OnPath 注册链路回调，连接建立时告知实际选中的候选对。
// 这是判断"音频到底走的哪条路"的唯一可靠依据 —— 页面上显示的"直连"
// 可能是一个 VPN 的虚拟网卡地址，那条路实际上还绕了半个地球。
func (r *Receiver) OnPath(fn func(string)) { r.onPath = fn }

// describePath 从 ICE 统计里还原出被选中的那条链路。
func describePath(pc *webrtc.PeerConnection) string {
	stats := pc.GetStats()
	for _, s := range stats {
		pair, ok := s.(webrtc.ICECandidatePairStats)
		if !ok || pair.State != webrtc.StatsICECandidatePairStateSucceeded {
			continue
		}
		local, lok := stats[pair.LocalCandidateID].(webrtc.ICECandidateStats)
		remote, rok := stats[pair.RemoteCandidateID].(webrtc.ICECandidateStats)
		if !lok || !rok {
			continue
		}
		return fmt.Sprintf("本端 %s %s:%d ←→ 对端 %s %s:%d  RTT=%.0fms",
			local.CandidateType, local.IP, local.Port,
			remote.CandidateType, remote.IP, remote.Port,
			pair.CurrentRoundTripTime*1000)
	}
	return "未能取得候选对"
}

// Answer 处理一个来自发送端的 SDP offer，返回 answer。
func (r *Receiver) Answer(offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	if r.excludeCGNAT {
		stripped, n := stripCGNATCandidates(offer.SDP)
		if n > 0 {
			offer.SDP = stripped
		}
	}

	api, err := r.buildAPI()
	if err != nil {
		return nil, err
	}
	cfg := webrtc.Configuration{ICEServers: r.iceServers}
	if r.forceRelay {
		cfg.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 PeerConnection: %w", err)
	}

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		pc.Close()
		return nil, fmt.Errorf("添加音频接收通道: %w", err)
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// RTCP 必须持续读走，否则 NACK / 接收报告会被静默丢弃。
		go func() {
			for {
				if _, _, err := receiver.ReadRTCP(); err != nil {
					return
				}
			}
		}()
		r.consume(track)
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if r.onState != nil {
			r.onState(state)
		}
		if state == webrtc.PeerConnectionStateConnected && r.onPath != nil {
			r.onPath(describePath(pc))
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			r.mu.Lock()
			if r.pc == pc {
				r.pc = nil
			}
			r.mu.Unlock()
			pc.Close()
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		pc.Close()
		return nil, fmt.Errorf("设置远端描述: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, fmt.Errorf("创建 answer: %w", err)
	}

	// 没有独立信令通道，只能等 ICE 收集完再把完整 SDP 一次性交回去。
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return nil, fmt.Errorf("设置本端描述: %w", err)
	}
	<-gathered

	// 新连接建好，旧的让位。
	r.mu.Lock()
	old := r.pc
	r.pc = pc
	r.mu.Unlock()
	if old != nil {
		old.Close()
	}

	// 在交回去的这一刻补上 Opus 开关：SetLocalDescription 之后 pion 不会再
	// 重新生成 SDP，改这份副本不影响本端已经建好的收流状态。
	final := *pc.LocalDescription()
	final.SDP = withOpusParams(final.SDP, r.opusParams())

	return &final, nil
}

// consume 把一条音频轨解码成 PCM，一直读到轨道结束。
func (r *Receiver) consume(track *webrtc.TrackRemote) {
	dec, err := opus.NewDecoder(SampleRate, Channels)
	if err != nil {
		return
	}

	pcm := make([]int16, maxFrameSamples*Channels)
	fec := make([]int16, maxFrameSamples*Channels)

	// 丢包与乱序的处理原则：只播按序前进的音频，缺口用编码器自带的冗余补。
	//
	//   迟到/重复的包（序号不比已见最大值新）直接丢弃。解码输出是顺序流，
	//   把迟到的 20ms 插在新音频后面，听感就是一声杂音，比丢了更糟。
	//
	//   恰好缺一个包时，用下一个包里捎带的 FEC 副本把缺帧解出来 ——
	//   这不是猜出来的音频，是编码器为上一帧存的低码率原件（useinbandfec
	//   就是为此协商的；只在 SDP 里声明、解码时不取用，等于白花带宽）。
	//   若缺口真是乱序造成的，副本已顶上原位，迟到的真身到达后照例丢弃，
	//   两条路径殊途同归，不会重复发声。
	//
	//   缺口更大时不做 PLC 连环脑补，留给播放侧的去咔哒处理软化边界。
	var maxSeq uint16
	lastN := 0 // 上一帧的每声道样本数，FEC 补帧时按它定缺帧长度
	first := true

	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				// 轨道断了，等下一次 offer 重建。
			}
			return
		}

		diff := int16(pkt.SequenceNumber - maxSeq)
		r.stats.observe(pkt.SequenceNumber, first, maxSeq)
		if first || diff > 0 {
			maxSeq = pkt.SequenceNumber
		}
		if !first && diff <= 0 {
			continue // 迟到或重复
		}
		gap := !first && diff == 2
		first = false

		if len(pkt.Payload) == 0 {
			continue
		}
		if gap && lastN > 0 {
			if err := dec.DecodeFEC(pkt.Payload, fec[:lastN*Channels]); err == nil {
				r.emit(fec[:lastN*Channels])
			}
		}
		n, err := dec.Decode(pkt.Payload, pcm)
		if err != nil {
			continue
		}
		lastN = n
		r.emit(pcm[:n*Channels])
	}
}

func (r *Receiver) emit(pcm []int16) {
	if r.onPCM != nil && len(pcm) > 0 {
		r.onPCM(pcm)
	}
}

// Close 断开当前连接。
func (r *Receiver) Close() {
	r.mu.Lock()
	pc := r.pc
	r.pc = nil
	r.mu.Unlock()
	if pc != nil {
		pc.Close()
	}
}
