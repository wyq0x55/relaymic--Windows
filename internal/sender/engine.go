// Package sender 是发送端的核心：采集本机麦克风，Opus 编码，WebRTC 推流。
//
// CLI（cmd/sender）和 GUI（cmd/sender-gui）共用这一份逻辑，
// 界面只负责把状态画出来。
package sender

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/hueshu/relaymic/internal/audio"
	"github.com/hueshu/relaymic/internal/discover"
)

const (
	sampleRate = 48000
	// 采集和编码都用单声道：麦克风本来就是 mono，立体声只是把同样的
	// 内容发两份。SDP 层仍声明 opus/48000/2 —— 真实声道数编在 Opus
	// 包内部，接收端的 libopus 会把 mono 自动复制成两路（见 internal/rtc）。
	channels    = 1
	frameMS     = 20
	frameSize   = sampleRate / 1000 * frameMS
	maxOpusSize = 4000
)

// Config 是一次推流的全部参数。
type Config struct {
	Targets  []string // 接收端地址列表，形如 https://100.x.y.z:7420。全部同时推流
	Discover bool     // 自动发现：扫描 tailnet 内的接收端并自动加入广播
	Device   string   // 输入设备名子串，空 = 系统默认
	Bitrate  int      // Opus 码率，0 = 96000
}

// link 是到一个接收端的连接。每个接收端独立打洞、独立重连，
// 一台挂了不影响其他台。
type link struct {
	target string

	mu    sync.Mutex
	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticSample // 当前活跃连接的音轨
}

func (l *link) currentTrack() *webrtc.TrackLocalStaticSample {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.track
}

// Engine 管理"采集 → 编码 → 多路连接"的完整生命周期。
// 采集和编码只做一次，同一帧扇出到所有活跃接收端 —— 用户在多台
// 机器间走动时不需要切换，每台的虚拟麦克风里都实时有声音。
// Start 之后各路自己维持重连；Stop 彻底收尾。
type Engine struct {
	cfg Config

	// OnState 在某一路连接状态变化时被调（带那一路的地址）；
	// OnLevel 每秒报一次采集峰值（dBFS）。
	// 回调来自内部协程，界面侧自己负责切回 UI 线程。
	OnState func(target, state string)
	OnLevel func(float64)

	mu       sync.Mutex
	actx     *audio.Context
	capturer *audio.Capturer
	links    map[string]*link // 目标集：手动配置 + 自动发现，运行中可增
	stopped  chan struct{}
	running  bool
}

// canonicalTarget 把地址归一成 host:port，作为目标集的 key。
// 手动填的和自动发现的同一台机器，字符串可能差在大小写、空格、
// 结尾斜杠 —— 归一化后同机必然去重。两条连接指向同一个接收端
// 会互相顶（接收端单发送端设计），表现就是"永远连接中"。
func canonicalTarget(t string) string {
	t = strings.TrimSpace(t)
	u, err := url.Parse(t)
	if err != nil || u.Host == "" {
		return strings.ToLower(t)
	}
	return strings.ToLower(u.Host)
}

// snapshotLinks 取当前目标集快照，编码协程每帧调用。
func (e *Engine) snapshotLinks() []*link {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*link, 0, len(e.links))
	for _, l := range e.links {
		out = append(out, l)
	}
	return out
}

// AddTarget 运行中添加一个接收端（按 host 幂等）。
// 自动发现和手动添加共用这条路。
func (e *Engine) AddTarget(target string) {
	key := canonicalTarget(target)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return
	}
	if _, ok := e.links[key]; ok {
		return
	}
	l := &link{target: strings.TrimSpace(target)}
	e.links[key] = l
	go e.connectLoop(l, e.stopped)
}

// ListMics 列出输入设备名，供界面做下拉框。
func ListMics() ([]string, error) {
	actx, err := audio.NewContext()
	if err != nil {
		return nil, err
	}
	defer actx.Close()
	devices, err := actx.Captures()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(devices))
	for _, d := range devices {
		names = append(names, d.Name)
	}
	return names, nil
}

func New(cfg Config) *Engine {
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = 96000
	}
	return &Engine{cfg: cfg}
}

func (e *Engine) state(target, s string) {
	if e.OnState != nil {
		e.OnState(target, s)
	}
}

// Start 打开麦克风并进入连接循环。非阻塞；失败时返回错误且不留资源。
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return nil
	}

	actx, err := audio.NewContext()
	if err != nil {
		return err
	}
	dev, err := actx.FindCapture(e.cfg.Device)
	if err != nil {
		actx.Close()
		return err
	}
	// BlackHole 是接收端的输出容器，不是麦克风。采集它就是把接收端
	// 播的声音再发回去 —— 一条完美的回环。宁可拒绝启动也不能默默成环。
	if strings.Contains(strings.ToLower(dev.Name), "blackhole") {
		actx.Close()
		return fmt.Errorf("%s 是虚拟回环设备，不是麦克风，请换一个输入设备", dev.Name)
	}

	// 编码器的开关一次拨到位，这正是原生发送端存在的意义：
	//   VoIP 模式 ／ FEC 开 ／ DTX 不开 ——
	// 静音判断交给下游识别引擎，编码器不该替它做主（浏览器就是在这儿掐掉了轻声）。
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		actx.Close()
		return err
	}
	if err := enc.SetBitrate(e.cfg.Bitrate); err != nil {
		actx.Close()
		return err
	}
	_ = enc.SetInBandFEC(true)
	_ = enc.SetPacketLossPerc(5)

	// 采集回调 → 帧缓冲 → 编码发送。回调是实时线程，只做拷贝；
	// 攒满 20ms 才编码，编码在普通协程里做。
	var bufMu sync.Mutex
	buf := make([]int16, 0, frameSize*4)
	level := 0.0
	pending := make(chan []int16, 8)
	stopped := make(chan struct{})

	capturer, err := actx.NewCapturer(dev, sampleRate, channels, func(pcm []int16) {
		bufMu.Lock()
		buf = append(buf, pcm...)
		for len(buf) >= frameSize {
			frame := make([]int16, frameSize)
			copy(frame, buf[:frameSize])
			buf = buf[frameSize:]
			select {
			case pending <- frame:
			default: // 编码跟不上就丢帧，绝不阻塞实时回调
			}
		}
		for _, s := range pcm {
			if v := math.Abs(float64(s)); v > level {
				level = v
			}
		}
		bufMu.Unlock()
	})
	if err != nil {
		actx.Close()
		return err
	}

	links := make(map[string]*link, len(e.cfg.Targets))
	for _, t := range e.cfg.Targets {
		key := canonicalTarget(t)
		if _, ok := links[key]; ok {
			continue // 同一台机器只留一条连接
		}
		links[key] = &link{target: strings.TrimSpace(t)}
	}

	go func() { // 编码协程：编一次，扇出到所有活跃接收端
		out := make([]byte, maxOpusSize)
		for {
			select {
			case <-stopped:
				return
			case frame := <-pending:
				n, err := enc.Encode(frame, out)
				if err != nil {
					continue
				}
				for _, l := range e.snapshotLinks() {
					track := l.currentTrack()
					if track == nil {
						continue // 这一路还没连上
					}
					// WriteSample 同步打包发送，不留 Data 引用，
					// 串行写完即可安全复用 out。
					_ = track.WriteSample(media.Sample{
						Data:     out[:n],
						Duration: frameMS * time.Millisecond,
					})
				}
			}
		}
	}()

	go func() { // 电平协程
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-t.C:
				bufMu.Lock()
				peak := level
				level = 0
				bufMu.Unlock()
				db := -96.0
				if peak > 0 {
					db = 20 * math.Log10(peak/math.MaxInt16)
				}
				if e.OnLevel != nil {
					e.OnLevel(db)
				}
			}
		}
	}()

	for _, l := range links {
		go e.connectLoop(l, stopped)
	}

	e.actx = actx
	e.capturer = capturer
	e.links = links
	e.stopped = stopped
	e.running = true

	// 自动发现：扫 tailnet 里开着接收端口的机器，出现即加入广播。
	// 新 Mac 跑完部署脚本，这里 30 秒内自动接上，界面不用填任何东西。
	// 只加不减：机器下线由该路的退避重连自己扛着，回来就恢复。
	if e.cfg.Discover {
		go func() {
			for {
				if found, err := discover.Receivers(); err == nil {
					for _, t := range found {
						e.AddTarget(t)
					}
				}
				select {
				case <-stopped:
					return
				case <-time.After(30 * time.Second):
				}
			}
		}()
	}
	return nil
}

// Stop 断开连接并释放麦克风。可重复调用。
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return
	}
	close(e.stopped)
	for _, l := range e.links {
		l.mu.Lock()
		if l.pc != nil {
			l.pc.Close()
			l.pc = nil
		}
		l.track = nil
		l.mu.Unlock()
	}
	e.links = nil
	e.capturer.Close()
	e.actx.Close()
	e.running = false
	if e.OnState != nil {
		for _, t := range e.cfg.Targets {
			e.OnState(t, "已停止")
		}
	}
}

// connectLoop 维持到一个接收端的连接，断了就退避重连。
// 网络恢复、接收端重启、电脑睡醒，都走同一条路径。
func (e *Engine) connectLoop(l *link, stopped <-chan struct{}) {
	retry := 0
	for {
		select {
		case <-stopped:
			return
		default:
		}
		dead, err := e.connectOnce(l, stopped)
		if err != nil {
			delay := backoff(retry)
			retry++
			e.state(l.target, fmt.Sprintf("连接失败：%v（%s 后重试）", err, delay))
			select {
			case <-time.After(delay):
				continue
			case <-stopped:
				return
			}
		}
		retry = 0
		select {
		case <-dead:
			e.state(l.target, "连接断开，重连中…")
		case <-stopped:
			return
		}
	}
}

// connectOnce 建一条到接收端的连接，返回"连接死亡"通知通道。
//
// 音轨每次连接都新建，绝不跨连接复用：把旧音轨重新绑到新 PeerConnection
// 上，曾出现过 ICE 已连通、RTP 却一个包都不发的静默故障（接收端重启后
// 的重连场景）。新轨新连接，状态从零开始，没有历史可以出错。
func (e *Engine) connectOnce(l *link, stopped <-chan struct{}) (<-chan struct{}, error) {
	e.state(l.target, "连接中…")
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: sampleRate,
			Channels:  2, // SDP 恒定声明，与包内声道数无关
		}, "audio", "sender")
	if err != nil {
		return nil, err
	}
	cfg := webrtc.Configuration{ICEServers: fetchICE(l.target)}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			pc.Close()
		}
	}()

	sender, err := pc.AddTrack(track)
	if err != nil {
		return nil, err
	}
	go func() { // RTCP 必须读走
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	connected := make(chan struct{})
	dead := make(chan struct{})
	var once sync.Once
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateConnected:
			e.state(l.target, "已连接")
			once.Do(func() { close(connected) })
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			select {
			case <-dead:
			default:
				close(dead)
				pc.Close()
			}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return nil, err
	}
	// 候选收集封顶 3 秒：STUN 不通时不陪它等超时。
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
	}

	answer, err := negotiate(l.target, pc.LocalDescription())
	if err != nil {
		return nil, err
	}
	if err := pc.SetRemoteDescription(*answer); err != nil {
		return nil, err
	}

	select {
	case <-connected:
		ok = true
		l.mu.Lock()
		l.pc = pc
		l.track = track // 编码协程从此写这条新轨
		l.mu.Unlock()
		return dead, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("30s 内没有连上")
	case <-dead:
		return nil, fmt.Errorf("连接建立失败")
	case <-stopped:
		return nil, fmt.Errorf("已停止")
	}
}

// fetchICE 从接收端拉 STUN/TURN 配置。拉不到就退回纯 STUN ——
// 两端必须用同一套 TURN，中继候选才配得上对。
func fetchICE(target string) []webrtc.ICEServer {
	// 兜底列表放多个:ICE 会并行探测,哪个通用哪个。国际与国内各留一条,
	// 免得换个地区就连不上。
	fallback := []webrtc.ICEServer{{URLs: []string{
		"stun:stun.l.google.com:19302",
		"stun:stun.cloudflare.com:3478",
		"stun:stun.miwifi.com:3478",
	}}}
	resp, err := insecureClient().Get(target + "/ice-config")
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	var cfg struct {
		ICEServers []struct {
			URLs       []string `json:"urls"`
			Username   string   `json:"username"`
			Credential string   `json:"credential"`
		} `json:"iceServers"`
	}
	if json.NewDecoder(resp.Body).Decode(&cfg) != nil || len(cfg.ICEServers) == 0 {
		return fallback
	}
	out := make([]webrtc.ICEServer, 0, len(cfg.ICEServers))
	for _, s := range cfg.ICEServers {
		out = append(out, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
	}
	return out
}

func negotiate(target string, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	body, err := json.Marshal(offer)
	if err != nil {
		return nil, err
	}
	resp, err := insecureClient().Post(target+"/offer", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("连接接收端: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("接收端返回 %s", resp.Status)
	}
	var answer webrtc.SessionDescription
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return nil, fmt.Errorf("解析 answer: %w", err)
	}
	return &answer, nil
}

// insecureClient 跳过证书校验：接收端用的是自签证书，
// 音频本身走 DTLS-SRTP 加密，这条信令通道要的只是可达。
func insecureClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func backoff(retry int) time.Duration {
	if retry > 4 {
		retry = 4
	}
	d := time.Second * time.Duration(1<<retry)
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}
