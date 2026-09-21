// receiver 跑在被远程控制的那台机器上。
//
// 它做两件事：主动连到公网控制面等发送端，收到音频后写进虚拟麦克风。
// 用户在任何吃麦克风的软件里选中那个虚拟设备，就能听到本地说的话。
//
// 它不监听任何端口 —— 公网入口只有控制面一个。这台机器在 NAT 或公司防火墙
// 后面也能用，因为整条控制通路都是它主动连出去的出站 443。
//
// 进程分成两半：常驻的本机控制台，和可起可停的运行实例。控制台先起来，
// 也因此活得比运行实例久 —— 设备没插、Hub 地址写错、凭据过期，这些都得
// 能在本机页面上看见并改掉，而不是让进程带着一行日志消失。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hueshu/relaymic/internal/audio"
	"github.com/hueshu/relaymic/internal/audiodevice"
	"github.com/hueshu/relaymic/internal/hubclient"
	"github.com/hueshu/relaymic/internal/monitor"
	"github.com/hueshu/relaymic/internal/receiverconfig"
	"github.com/hueshu/relaymic/internal/rtc"
	"github.com/hueshu/relaymic/internal/signaling"
	"github.com/hueshu/relaymic/internal/turnshim"
	"github.com/hueshu/relaymic/internal/web"
	"github.com/pion/webrtc/v4"
)

func main() {
	opts := parseFlags()

	log.SetFlags(log.Ltime)

	// AGPL 说的 "Appropriate Legal Notices"：启动时把版权、无担保、
	// 以及源码在哪告诉用户一次。命令行程序的惯例做法。
	log.Println("RelayMic  Copyright (C) 2026 Shu Chunhui")
	log.Println("本程序不提供任何担保，遵循 AGPL-3.0 发布。")
	log.Println("源码：https://github.com/hueshu/relaymic")

	c := &console{
		opts:     opts,
		st:       &statusState{state: "启动中"},
		pairing:  monitor.NewPairingState(),
		sessions: monitor.NewSessionState(),
	}
	// 控制台先起：它只读状态、不发音频，运行实例起不来时它照样要能开。
	defer closeMonitor(c.serve())

	if actx, err := audio.NewContext(); err != nil {
		c.fail(fmt.Errorf("音频初始化失败: %w", err))
	} else {
		defer actx.Close()
		c.actx = actx
		if err := c.startRuntime(); err != nil {
			c.fail(err)
		}
	}

	// 启动失败也走这里：进程留住，控制台继续服务，人能进页面看到原因。
	waitForSignal()
	log.Println("退出")
	c.stopRuntime()
}

// options 是这次运行的命令行配置。
type options struct {
	hub            string
	token          string
	tokenFile      string
	monitorAddr    string
	device         string
	returnDevice   string
	returnLoopback string
	bufferMS       int
	gain           float64
	meter          bool
	noCGNAT        bool
	stun           string
	turn           string
	turnUser       string
	turnPass       string
	forceRelay     bool
	turnTunnel     bool
	dtx            bool
	record         string
}

func parseFlags() options {
	hub := flag.String("hub", "", "公网控制面地址，形如 wss://mic.example.com/ws/receiver")
	token := flag.String("token", "", "接收端凭据；也可以用 -token-file")
	tokenFile := flag.String("token-file", "", "从文件读接收端凭据（取首行）")
	monitorAddr := flag.String("monitor", "", "本机诊断页监听地址，形如 127.0.0.1:7420；留空表示不监听任何端口")
	returnDevice := flag.String("return-device", "", "回传：采集这个录制设备（第二条虚拟线，例如 CABLE-A Output）")
	returnLoopback := flag.String("return-loopback", "", "回传：环回采集这个播放设备（例如 ヘッドホン）；会带上该设备的全部系统声音")
	deviceName := flag.String("device", receiverconfig.DefaultOutputDeviceForOS(runtime.GOOS), "输出设备名（子串匹配）；Windows 默认匹配 VB-CABLE")
	// 150ms 是实测值：80ms 扛不住 WiFi 突发，600ms 白垫延迟。
	bufferMS := flag.Int("buffer", receiverconfig.DefaultBufferMS, "抖动缓冲目标深度（毫秒）")
	gain := flag.Float64("gain", 0, "固定增益倍数；留空或 0 表示用自动增益（AGC）")
	meter := flag.Bool("meter", false, "每秒打印一次收到的音频电平，用来诊断音量")
	// 默认不排除：对称型 NAT 下没有 TURN 就打不通，排掉覆盖网等于自断退路。
	// 配好 TURN 之后再开这个开关，才能真正甩掉绕地球的中继。
	noCGNAT := flag.Bool("no-cgnat", false, "排除 100.64.0.0/10 候选，逼 ICE 走公网直连。需先配好 -turn")
	// 默认用国内可达的 STUN：google 的在国内不通，而 answer 要等收集完才发，
	// STUN 不通的代价是每次连接白等一个超时，不是"少个候选"那么便宜。
	stun := flag.String("stun", "stun:stun.l.google.com:19302,stun:stun.cloudflare.com:3478,stun:stun.miwifi.com:3478", "STUN 服务器，逗号分隔")
	turn := flag.String("turn", "", "TURN 地址，形如 turn:host:3478")
	turnUser := flag.String("turn-user", "", "TURN 用户名")
	turnPass := flag.String("turn-pass", "", "TURN 密码")
	forceRelay := flag.Bool("force-relay", false, "只用 TURN 中继候选，用于验证中继链路")
	turnTunnel := flag.Bool("turn-tunnel", false, "把 TURN 流量经控制面隧道转发（只放行 HTTP 代理的网络需要）")
	// 默认关：DTX 的 VAD 会把低电平语音误判成静音掐掉，实测每秒断一次。
	// 语音识别场景带宽根本不是瓶颈，没有理由为省包冒断字的险。
	dtx := flag.Bool("dtx", false, "让发送端静音时停发包（省带宽，但可能掐掉轻声）")
	record := flag.String("record", "", "把解码后、处理前的原始 PCM 录成 WAV，用于杂音诊断")
	flag.Parse()

	return options{
		hub:            *hub,
		token:          *token,
		tokenFile:      *tokenFile,
		monitorAddr:    *monitorAddr,
		device:         *deviceName,
		returnDevice:   *returnDevice,
		returnLoopback: *returnLoopback,
		bufferMS:       *bufferMS,
		gain:           *gain,
		meter:          *meter,
		noCGNAT:        *noCGNAT,
		stun:           *stun,
		turn:           *turn,
		turnUser:       *turnUser,
		turnPass:       *turnPass,
		forceRelay:     *forceRelay,
		turnTunnel:     *turnTunnel,
		dtx:            *dtx,
		record:         *record,
	}
}

// console 是本机的常驻控制台。它比运行实例活得久。
type console struct {
	opts     options
	actx     *audio.Context
	st       *statusState
	pairing  *monitor.PairingState
	sessions *monitor.SessionState

	mu sync.Mutex
	rt *instance
}

func (c *console) current() *instance {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rt
}

func (c *console) publish(rt *instance) {
	c.mu.Lock()
	c.rt = rt
	c.mu.Unlock()
}

// fail 把启动失败留在控制台上，而不是让进程消失。
func (c *console) fail(err error) {
	c.st.setState("未启动")
	c.st.setError(err.Error())
	log.Println("接收端未启动:", err)
}

// startRuntime 建一个运行实例并接上控制台。失败时先把它收拾干净再返回错误 ——
// 半启动的实例比没启动更糟：设备占着、连接挂着，页面却看不到。
func (c *console) startRuntime() error {
	if c.actx == nil {
		return errors.New("音频子系统不可用")
	}
	rt := &instance{opts: c.opts, actx: c.actx, st: c.st, pairing: c.pairing, sessions: c.sessions}
	if err := rt.start(); err != nil {
		rt.stop()
		return err
	}
	c.publish(rt)
	return nil
}

func (c *console) stopRuntime() {
	if rt := c.current(); rt != nil {
		rt.stop()
	}
}

// serve 起本机诊断页。默认不监听任何端口，要开就自己指定绑到哪。
// 它只给运维看波形和统计，从不参与信令，因此也不影响"接收端只出站"。
func (c *console) serve() *http.Server {
	if c.opts.monitorAddr == "" {
		return nil
	}
	srv := &http.Server{Addr: c.opts.monitorAddr, Handler: c.routes()}
	go func() {
		log.Printf("本机诊断页: http://%s/monitor", c.opts.monitorAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Println("诊断页已停止:", err)
		}
	}()
	return srv
}

func closeMonitor(srv *http.Server) {
	if srv != nil {
		_ = srv.Close()
	}
}

func (c *console) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/monitor", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.MonitorHTML)
	})
	mux.HandleFunc("/api/status", c.status)
	// 本机写操作：只允许回环 + 自定义头。只绑回环挡不住 CSRF ——
	// 浏览器里任何一个页面都能往 127.0.0.1 发 POST。
	mux.HandleFunc("POST /api/session/close", c.closeSession)
	return mux
}

// status 汇总页面要看的全部状态。
//
// 缓冲和 RTP 计数现场取：它们本来就带锁，再抄一份到状态里只会多一处会过期的
// 真相。运行实例还没起来时，这里只报配置和故障原因。
func (c *console) status(w http.ResponseWriter, r *http.Request) {
	view := c.st.snapshot()
	pairingView := c.pairing.Snapshot(time.Now(), monitor.IsLoopbackRemoteAddr(r.RemoteAddr))

	out := map[string]any{
		"state":         view.state,
		"error":         view.err,
		"path":          view.path,
		"levelDb":       view.levelDB,
		"returnLevelDb": view.returnDB,
		"gain":          view.gain,
		"hub":           c.opts.hub,
		"outputDevice":  c.opts.device,
		"returnSource":  "",
		"returnMode":    "未配置",
		"forceRelay":    c.opts.forceRelay,
		"turnTunnel":    c.opts.turnTunnel,
		"bufferedMs":    0,
		"dropped":       0,
		"starved":       0,
		"received":      0,
		"lost":          0,
		"sessionActive": c.sessions.Get() != "",
		"pairing": map[string]any{
			"active":       pairingView.Active,
			"code":         pairingView.Code,
			"hidden":       pairingView.Hidden,
			"expiresInSec": int(math.Ceil(pairingView.ExpiresIn.Seconds())),
		},
	}
	if rt := c.current(); rt != nil && rt.player != nil && rt.receiver != nil {
		buffered, dropped, starved := rt.player.Stats()
		received, lost, _, _ := rt.receiver.Stats().Snapshot()
		out["bufferedMs"] = buffered * 1000 / (rtc.SampleRate * rtc.Channels)
		out["dropped"] = dropped
		out["starved"] = starved
		out["received"] = received
		out["lost"] = lost
		out["outputDevice"] = rt.dev.Name
		out["returnSource"] = rt.returnSourceName
		out["returnMode"] = rt.returnSourceMode
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// closeSession 结束当前通话：让控制面把这次会话收掉，接收端不用重启。
func (c *console) closeSession(w http.ResponseWriter, r *http.Request) {
	if !localWriteAllowed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	session := c.sessions.Get()
	if session == "" {
		http.Error(w, "没有进行中的通话", http.StatusConflict)
		return
	}
	rt := c.current()
	if rt == nil || rt.client == nil {
		http.Error(w, "接收端未启动", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := rt.client.CloseSession(ctx, session); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Println("本机控制台结束了当前通话")
	w.WriteHeader(http.StatusNoContent)
}

// waitForSignal 一直阻塞到用户按下 Ctrl+C。
func waitForSignal() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	signal.Stop(stop)
}

// instance 是一次接收端运行实例：输出设备、WebRTC 接收、控制面连接。
//
// 控制台常驻，运行实例可起可停 —— "改设置"的语义是换一个实例，
// 而不是让进程消失。
type instance struct {
	opts     options
	actx     *audio.Context
	st       *statusState
	pairing  *monitor.PairingState
	sessions *monitor.SessionState

	dev      audio.Device
	player   *audio.Player
	receiver *rtc.Receiver
	client   *hubclient.Client
	capturer *audio.Capturer
	shim     *turnshim.Shim
	rec      *audio.WAVWriter

	level       *levelMeter
	returnLevel *levelMeter
	agc         *audio.AGC
	useAGC      bool

	returnSourceName string
	returnSourceMode string

	cancel  context.CancelFunc
	stopped sync.Once
	wg      sync.WaitGroup
}

func (r *instance) start() error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	dev, err := r.actx.FindPlayback(r.opts.device)
	if err != nil {
		return err
	}
	r.dev = dev
	r.st.setState("打开输出设备中")

	// 打开设备失败不退出，进程内重试。
	//
	// 之前失败就 Fatalln，靠 launchd 重启 —— 但超时那一刻还挂着一个
	// 没法取消的 InitDevice cgo 调用，进程退出等于把它强杀在 coreaudiod
	// 里；"超时→退出→重启"每循环一轮就多攒一个残留，越试越打不开，
	// 最后只能 sudo killall coreaudiod。留在进程里重试，残留至多一个。
	//
	// 每轮先让系统的 say 打开一次设备：macOS 26 上 BlackHole 空置会
	// 回到"未激活"态，非 Apple 进程首开会挂死；say 能唤醒它。
	for {
		if runtime.GOOS == "darwin" {
			_ = exec.Command("/usr/bin/say", "-a", dev.Name, " ").Run()
		}
		// 解码出来就是立体声交错的 PCM，和 BlackHole 2ch 的格式一致，直接灌进去。
		r.player, err = r.actx.NewPlayer(dev, rtc.SampleRate, rtc.Channels, r.opts.bufferMS)
		if err == nil {
			break
		}
		log.Println(err)
		log.Println("30 秒后重试打开设备（进程不退出，避免累积驱动残留）")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}

	// 增益补在解码之后、写设备之前。发送端的麦克风音量差异很大，
	// 而下游的语音识别普遍带静音门限 —— 声音送到了但太小，表现和没送到一样。
	r.level = newLevelMeter()
	r.agc = audio.NewAGC()
	r.useAGC = r.opts.gain <= 0
	if r.useAGC {
		log.Println("自动增益已启用")
	} else {
		log.Printf("固定增益 %.2gx", r.opts.gain)
	}

	// 诊断录音挂在链条最前面：录的是解码器吐出的原始样本。
	// 波形里若已有硬边缘，病在发送端或传输；若这里干净而听感仍炸，
	// 病在后面的处理和播放。这一刀把整条链切成两半。
	if r.opts.record != "" {
		rec, err := audio.NewWAVWriter(r.opts.record, rtc.SampleRate, rtc.Channels)
		if err != nil {
			return fmt.Errorf("打开录音文件失败: %w", err)
		}
		r.rec = rec
		log.Println("诊断录音:", r.opts.record)
	}

	r.receiver = rtc.New(
		func(pcm []int16) {
			if r.rec != nil {
				r.rec.Write(pcm)
			}
			if r.useAGC {
				r.agc.Process(pcm)
			} else {
				applyGain(pcm, r.opts.gain)
			}
			r.level.observe(pcm)
			r.player.Write(pcm)
		},
		func(state webrtc.PeerConnectionState) {
			log.Println("连接状态:", state)
			r.st.setState(state.String())
		},
	)
	r.receiver.OnPath(func(path string) {
		log.Println("链路:", path)
		r.st.setPath(path)
	})
	r.receiver.ExcludeCGNAT(r.opts.noCGNAT)
	r.receiver.SetICEServers(iceServers(r.opts.stun, r.opts.turn, r.opts.turnUser, r.opts.turnPass))
	r.receiver.SetDTX(r.opts.dtx)
	r.receiver.ForceRelay(r.opts.forceRelay)
	if r.opts.forceRelay {
		log.Println("强制中继模式：只接受 TURN 候选")
	}
	if r.opts.noCGNAT {
		log.Println("已排除 CGNAT(100.64/10) 候选：强制公网直连")
		if r.opts.turn == "" {
			log.Println("警告：没有配 TURN，对称型 NAT 下很可能完全连不上")
		}
	}

	// 回传：把公司电脑上的声音送回发送端。
	//
	// 两条路语义差得很远，必须显式选一条：采第二条虚拟线只拿到目标应用的声音；
	// 环回采播放设备会把这台机器上所有系统声音一起送进会议。
	returnSrc, err := resolveReturnSource(r.opts.returnDevice, r.opts.returnLoopback)
	if err != nil {
		return err
	}
	if err := returnSrc.checkNotFeedback(dev.Name); err != nil {
		return err
	}
	// 回传电平：和上行一样单独取，用来回答"对面到底有没有声音进来"。
	// 没有这个读数，"听不到会议"只能靠猜。
	r.returnSourceMode = "未配置"
	if returnSrc.enabled() {
		downlink, err := rtc.NewDownlink()
		if err != nil {
			return fmt.Errorf("建立回传链路失败: %w", err)
		}
		r.receiver.SetDownlink(downlink)
		r.returnLevel = newLevelMeter()
		onReturnPCM := func(pcm []int16) {
			r.returnLevel.observe(pcm)
			downlink.Write(pcm)
		}

		var captureDev audio.Device
		if returnSrc.loopback {
			captureDev, err = r.actx.FindLoopback(returnSrc.selector)
			if err != nil {
				return err
			}
			r.capturer, err = r.actx.NewLoopbackCapturer(captureDev, rtc.SampleRate, rtc.Channels, onReturnPCM)
		} else {
			captureDev, err = r.actx.FindCapture(returnSrc.selector)
			if err != nil {
				return err
			}
			r.capturer, err = r.actx.NewCapturer(captureDev, rtc.SampleRate, rtc.Channels, onReturnPCM)
		}
		if err != nil {
			return fmt.Errorf("打开回传采集失败: %w", err)
		}
		if returnSrc.loopback {
			r.returnSourceMode = "环回"
			log.Printf("回传: 环回采集 %s（含这台机器的全部系统声音）", captureDev.Name)
		} else {
			r.returnSourceMode = "虚拟线"
			log.Printf("回传: 采集 %s", captureDev.Name)
		}
		r.returnSourceName = captureDev.Name
	}
	r.receiver.OnNotice(func(msg string) { log.Println(msg) })

	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	if name == "" {
		name = "RelayMic 接收端"
	}
	log.Println("机器名:", name)

	// 接收端不监听任何端口：公网入口只有控制面一个。
	// 这台机器在 NAT / 公司防火墙后面也能用，因为整条控制通路都是出站 443。
	if r.opts.hub == "" {
		return errors.New("缺少 -hub：接收端必须主动连到公网控制面，不再在本机监听端口")
	}
	secret, err := readToken(r.opts.token, r.opts.tokenFile)
	if err != nil {
		return err
	}
	client, err := hubclient.New(hubclient.Config{URL: r.opts.hub, Token: secret})
	if err != nil {
		return err
	}
	r.client = client

	// TURN 隧道：只放行 HTTP 代理的网络里，TURN 客户端自己出不去。
	// 在回环地址上开一个入口，把 TURN/TCP 塞进这条已经能通的 WSS。
	turnTunnelAddr := ""
	if r.opts.turnTunnel {
		shim, err := turnshim.Start(ctx, "127.0.0.1:0", client.DialTunnel)
		if err != nil {
			return fmt.Errorf("启动 TURN 隧道失败: %w", err)
		}
		r.shim = shim
		turnTunnelAddr = shim.Addr()
		log.Printf("TURN 隧道: %s → %s", turnTunnelAddr, r.opts.hub)
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		err := client.Run(ctx, hubclient.Handlers{
			OnCode: func(code string, expiresIn time.Duration) {
				log.Printf("已连接 %s", r.opts.hub)
				log.Printf("设备: %s", dev.Name)
				r.pairing.Set(code, time.Now().Add(expiresIn))
				if r.opts.monitorAddr == "" {
					log.Printf("已生成一次性配对码（%d 分钟有效）；用 -monitor 127.0.0.1:7420 在本机查看", int(expiresIn.Minutes()))
				}
				r.st.setState("等待发送端")
			},
			OnJoined: func(session string, sessionICE []signaling.ICEServer) {
				r.pairing.Clear()
				r.sessions.Set(session)
				if len(sessionICE) > 0 {
					servers := sessionICE
					if turnTunnelAddr != "" {
						servers = tunnelICEServers(sessionICE, turnTunnelAddr)
					}
					r.receiver.SetICEServers(hubICEServers(servers))
					log.Printf("已应用控制面 ICE 配置（%d 项）", len(sessionICE))
				}
				log.Println("发送端已接入")
			},
			OnLeft: func(session string) {
				r.pairing.Clear()
				r.sessions.Clear(session)
				log.Println("发送端已离开，控制面已换新配对码")
			},
			OnError: func(message string) {
				log.Println("控制面:", message)
			},
			// SDP 只在两端之间走：控制面原样转发，这里也只做编解码转换，
			// 不重新协商、不改写候选。
			OnOffer: func(offerCtx context.Context, session string, offer json.RawMessage) (json.RawMessage, error) {
				var sdp webrtc.SessionDescription
				if err := json.Unmarshal(offer, &sdp); err != nil {
					return nil, fmt.Errorf("解析 offer: %w", err)
				}
				answer, err := r.receiver.Answer(sdp)
				if err != nil {
					return nil, err
				}
				return json.Marshal(answer)
			},
		})
		if err != nil {
			// 握手被拒这类错误不会自愈：token 不对、地址不对，重连一万次
			// 也是同一个结果。留在控制台上让人看见，而不是安静地退避重连。
			r.st.setState("控制面拒绝连接")
			r.st.setError(err.Error())
			log.Println("信令连接失败:", err)
		}
	}()

	// 每 10 秒报一次缓冲健康度，用来判断要不要调 -buffer。
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		lastStarved := 0
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			buffered, dropped, starved := r.player.Stats()
			recv, lost, reorder, dup := r.receiver.Stats().Snapshot()
			if recv == 0 {
				continue
			}
			log.Printf("缓冲=%d(%.0fms) 丢弃=%d 欠载=%d ｜ RTP 收=%d 丢=%d(%.2f%%) 乱序=%d 重复=%d",
				buffered, float64(buffered)*1000/float64(rtc.SampleRate*rtc.Channels),
				dropped, starved, recv, lost,
				float64(lost)*100/float64(recv+lost), reorder, dup)
			// 欠载又涨了就把见底现场打出来："缓冲够深却见底"这种
			// 矛盾，靠 10 秒采样永远解释不了，只能靠现场数字。
			if starved > lastStarved {
				size, want := r.player.LastStarve()
				log.Printf("  最近欠载现场：缓冲剩 %d 样本(%.0fms)，声卡要 %d",
					size, float64(size)*1000/float64(rtc.SampleRate*rtc.Channels), want)
			}
			lastStarved = starved
		}
	}()

	// 电平表只有一个消费者：takeDBFS 会清零，监控页和 -meter 各取一次的话，
	// 两边都只能看到半截读数。这里统一取，再决定要不要打日志。
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			g := r.opts.gain
			if r.useAGC {
				g = r.agc.Gain()
			}
			db, ok := r.level.takeDBFS()
			if !ok {
				// 这一秒一个样本都没来，等同于静音，否则页面会一直挂着上一次的读数。
				db = -120
			}
			r.st.setLevel(db, g)
			if r.returnLevel != nil {
				// 回传的电平每秒都要取走，否则峰值会一直累加到下一次有人看。
				rdb, rok := r.returnLevel.takeDBFS()
				if !rok {
					rdb = -120
				}
				r.st.setReturnLevel(rdb)
				if rok && r.opts.meter {
					log.Printf("回传 %6.1f dBFS %s", rdb, bar(rdb))
				}
			}
			if !r.opts.meter || !ok {
				continue
			}
			if r.useAGC {
				log.Printf("电平 %6.1f dBFS %s  增益 %.1fx", db, bar(db), r.agc.Gain())
			} else {
				log.Printf("电平 %6.1f dBFS %s", db, bar(db))
			}
		}
	}()

	r.st.setState("连接控制面")
	return nil
}

// stop 关掉这个运行实例并释放它占的设备。可重复调用。
//
// 先取消上下文、等后台协程收工，最后才关设备 —— 反过来会在设备已经关掉
// 之后还有协程往里写。
func (r *instance) stop() {
	r.stopped.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		r.wg.Wait()
		if r.shim != nil {
			_ = r.shim.Close()
		}
		if r.capturer != nil {
			r.capturer.Close()
		}
		if r.receiver != nil {
			r.receiver.Close()
		}
		if r.player != nil {
			r.player.Close()
		}
		if r.rec != nil {
			_ = r.rec.Close()
		}
	})
}

// statusState 是 /api/status 里那几个"只有回调和定时器知道"的值。
// 写它的是 ICE 回调和电平协程，读它的是 HTTP 处理器，全在不同的协程上。
type statusState struct {
	mu       sync.Mutex
	state    string
	path     string
	err      string
	levelDB  float64
	returnDB float64
	gain     float64
}

// statusView 是一次性快照，避免处理器拿着锁去拼 JSON。
type statusView struct {
	state    string
	path     string
	err      string
	levelDB  float64
	returnDB float64
	gain     float64
}

func (s *statusState) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

func (s *statusState) setPath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
}

// setError 记下一次不会自愈的故障，让页面能说出"为什么没起来"。
func (s *statusState) setError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = msg
}

func (s *statusState) setLevel(db, gain float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.levelDB, s.gain = db, gain
}

func (s *statusState) setReturnLevel(db float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.returnDB = db
}

func (s *statusState) snapshot() statusView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return statusView{
		state:    s.state,
		path:     s.path,
		err:      s.err,
		levelDB:  s.levelDB,
		returnDB: s.returnDB,
		gain:     s.gain,
	}
}

// iceServers 组装 STUN/TURN 列表。TURN 只在填了地址时加入。
func iceServers(stun, turn, user, pass string) []webrtc.ICEServer {
	var out []webrtc.ICEServer
	for _, u := range strings.Split(stun, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, webrtc.ICEServer{URLs: []string{u}})
		}
	}
	if turn != "" {
		out = append(out, webrtc.ICEServer{
			URLs:       []string{turn},
			Username:   user,
			Credential: pass,
		})
	}
	return out
}

func hubICEServers(servers []signaling.ICEServer) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(servers))
	for _, server := range servers {
		if len(server.URLs) == 0 {
			continue
		}
		out = append(out, webrtc.ICEServer{
			URLs:       append([]string(nil), server.URLs...),
			Username:   server.Username,
			Credential: server.Credential,
		})
	}
	return out
}

// localWriteAllowed 判断一个本机写操作能不能放行。
//
// 只绑回环挡不住 CSRF：浏览器里任何一个页面都能往 127.0.0.1 发 POST。
// 所以再要求一个自定义头（HTML 表单发不出来），并校验来源与 Host 一致。
func localWriteAllowed(r *http.Request) bool {
	if !monitor.IsLoopbackRemoteAddr(r.RemoteAddr) {
		return false
	}
	if r.Header.Get("X-RelayMic") == "" {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

// tunnelICEServers 把 TURN 地址换成本机的隧道入口。
//
// 只有这台机器改走隧道 —— 它没别的路；远端浏览器仍用 Hub 下发的原始地址。
// STUN 保持原样：它失败只是拖慢收集，不是连不上的原因。
func tunnelICEServers(servers []signaling.ICEServer, localAddr string) []signaling.ICEServer {
	out := make([]signaling.ICEServer, 0, len(servers))
	for _, server := range servers {
		urls := make([]string, 0, len(server.URLs))
		for _, rawURL := range server.URLs {
			lower := strings.ToLower(strings.TrimSpace(rawURL))
			if strings.HasPrefix(lower, "turn:") || strings.HasPrefix(lower, "turns:") {
				urls = append(urls, "turn:"+localAddr+"?transport=tcp")
				continue
			}
			urls = append(urls, rawURL)
		}
		out = append(out, signaling.ICEServer{URLs: urls, Username: server.Username, Credential: server.Credential})
	}
	return out
}

// applyGain 就地放大 PCM，并在会削顶时先按整帧的峰值收回增益。
//
// 不能把超过 int16 范围的样本简单钉在边界：那会把响一点的元音、
// 爆破音切成平顶波，听起来就是杂音，语音识别也会丢字。整帧等比例
// 预限幅保留了波形形状；正常偏小的语音仍然得到完整的固定增益。
func applyGain(pcm []int16, gain float64) {
	if len(pcm) == 0 || gain == 1.0 {
		return
	}

	var peak float64
	for _, s := range pcm {
		v := math.Abs(float64(s))
		if v > peak {
			peak = v
		}
	}
	// 留约 1 dB 的余量，避免后续设备转换时再次碰到满刻度。
	const ceiling = 0.8912509381337456 * math.MaxInt16
	if peak > 0 && peak*gain > ceiling {
		gain = ceiling / peak
	}
	for i, s := range pcm {
		v := float64(s) * gain
		if v > math.MaxInt16 {
			v = math.MaxInt16
		} else if v < math.MinInt16 {
			v = math.MinInt16
		}
		pcm[i] = int16(v)
	}
}

// levelMeter 累计一秒内的峰值，供诊断"声音到底有没有到、够不够大"。
type levelMeter struct {
	mu    sync.Mutex
	peak  int16
	count int
}

func newLevelMeter() *levelMeter { return &levelMeter{} }

func (m *levelMeter) observe(pcm []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range pcm {
		if s < 0 {
			s = -s
		}
		if s > m.peak {
			m.peak = s
		}
	}
	m.count += len(pcm)
}

// takeDBFS 返回过去一段时间的峰值电平并复位。没有样本时返回 false。
func (m *levelMeter) takeDBFS() (float64, bool) {
	m.mu.Lock()
	peak, count := m.peak, m.count
	m.peak, m.count = 0, 0
	m.mu.Unlock()

	if count == 0 {
		return 0, false
	}
	if peak == 0 {
		return -120, true
	}
	return 20 * math.Log10(float64(peak)/math.MaxInt16), true
}

// bar 把 dBFS 画成一条方便扫一眼的横条。-60dB 以下基本等于静音。
func bar(db float64) string {
	n := int((db + 60) / 3)
	if n < 0 {
		n = 0
	}
	if n > 20 {
		n = 20
	}
	return strings.Repeat("█", n)
}

// readToken 取接收端凭据。
//
// 命令行参数会出现在进程列表里，任何能跑 ps 的账号都能看见；
// 生产部署应该用 -token-file，文件权限就是凭据的权限边界。
func readToken(token, tokenFile string) (string, error) {
	if token != "" && tokenFile != "" {
		return "", errors.New("同时给了 -token 和 -token-file，只能选一个")
	}
	if token != "" {
		return token, nil
	}
	if tokenFile == "" {
		return "", errors.New("缺少接收端凭据：用 -token 或 -token-file")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("读取凭据文件 %s: %w", tokenFile, err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", fmt.Errorf("凭据文件 %s 是空的", tokenFile)
	}
	return secret, nil
}

// returnSource 描述回传的声音从哪来。
type returnSource struct {
	loopback bool
	selector string
}

func (s returnSource) enabled() bool { return s.selector != "" }

// resolveReturnSource 决定回传采集源。
//
// 两个都填是配置错误，不猜：一条只采目标应用的虚拟线，另一条会把整机系统声音
// 一起送进会议 —— 语义差得太远，替用户选哪个都可能不对。
func resolveReturnSource(device, loopback string) (returnSource, error) {
	switch {
	case device != "" && loopback != "":
		return returnSource{}, errors.New("同时给了 -return-device 和 -return-loopback，只能选一个")
	case device != "":
		return returnSource{selector: device}, nil
	case loopback != "":
		return returnSource{loopback: true, selector: loopback}, nil
	default:
		return returnSource{}, nil
	}
}

// checkNotFeedback 拦住"采自己正在写的那条线"。
// 那不是回传，是把输出绕回输入，结果是啸叫。
func (s returnSource) checkNotFeedback(playbackName string) error {
	if !s.enabled() {
		return nil
	}
	// 采集侧和播放侧的名字本来就不同（CABLE Input / CABLE Output），
	// 所以比的是规范化后的子串包含关系，任一侧命中就算撞上。
	want := audiodevice.NormalizeName(s.selector)
	target := audiodevice.NormalizeName(playbackName)
	if want == "" || target == "" {
		return nil
	}
	if strings.Contains(target, want) || strings.Contains(want, target) {
		return fmt.Errorf("回传采集源 %q 和播放目标 %q 是同一个设备：那是环，不是回传", s.selector, playbackName)
	}
	return nil
}
