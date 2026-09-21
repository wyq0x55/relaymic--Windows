// receiver 跑在被远程控制的那台机器上。
//
// 它做两件事：主动连到公网控制面等发送端，收到音频后写进虚拟麦克风。
// 用户在任何吃麦克风的软件里选中那个虚拟设备，就能听到本地说的话。
//
// 它不监听任何端口 —— 公网入口只有控制面一个。这台机器在 NAT 或公司防火墙
// 后面也能用，因为整条控制通路都是它主动连出去的出站 443。
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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hueshu/relaymic/internal/audio"
	"github.com/hueshu/relaymic/internal/audiodevice"
	"github.com/hueshu/relaymic/internal/hubclient"
	"github.com/hueshu/relaymic/internal/receiverconfig"
	"github.com/hueshu/relaymic/internal/rtc"
	"github.com/hueshu/relaymic/internal/signaling"
	"github.com/hueshu/relaymic/internal/web"
	"github.com/pion/webrtc/v4"
)

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".relaymic"
	}
	return filepath.Join(home, ".config", "relaymic")
}

func defaultSegmentsDir() string { return filepath.Join(defaultDataDir(), "recordings") }

func main() {
	hub := flag.String("hub", "", "公网控制面地址，形如 wss://mic.example.com/ws/receiver")
	token := flag.String("token", "", "接收端凭据；也可以用 -token-file")
	tokenFile := flag.String("token-file", "", "从文件读接收端凭据（取首行）")
	monitor := flag.String("monitor", "", "本机诊断页监听地址，形如 127.0.0.1:7420；留空表示不监听任何端口")
	returnDevice := flag.String("return-device", "", "回传：采集这个录制设备（第二条虚拟线，例如 CABLE-A Output）")
	returnLoopback := flag.String("return-loopback", "", "回传：环回采集这个播放设备（例如 ヘッドホン）；会带上该设备的全部系统声音")
	deviceName := flag.String("device", receiverconfig.DefaultOutputDeviceForOS(runtime.GOOS), "输出设备名（子串匹配）；Windows 默认匹配 VB-CABLE")
	// 150ms 是实测值：80ms 扛不住 WiFi 突发，600ms 白垫延迟。
	bufferMS := flag.Int("buffer", 150, "抖动缓冲目标深度（毫秒）")
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
	// 默认关：DTX 的 VAD 会把低电平语音误判成静音掐掉，实测每秒断一次。
	// 语音识别场景带宽根本不是瓶颈，没有理由为省包冒断字的险。
	dtx := flag.Bool("dtx", false, "让发送端静音时停发包（省带宽，但可能掐掉轻声）")
	record := flag.String("record", "", "把解码后、处理前的原始 PCM 录成 WAV，用于杂音诊断")
	segmentsDir := flag.String("segments-dir", defaultSegmentsDir(), "按语音段切片存 WAV 的目录，供监控页回听；留空表示不录")
	flag.Parse()

	log.SetFlags(log.Ltime)

	// AGPL 说的 "Appropriate Legal Notices"：启动时把版权、无担保、
	// 以及源码在哪告诉用户一次。命令行程序的惯例做法。
	log.Println("RelayMic  Copyright (C) 2026 Shu Chunhui")
	log.Println("本程序不提供任何担保，遵循 AGPL-3.0 发布。")
	log.Println("源码：https://github.com/hueshu/relaymic")

	actx, err := audio.NewContext()
	if err != nil {
		log.Fatalln("音频初始化失败:", err)
	}
	defer actx.Close()

	dev, err := actx.FindPlayback(*deviceName)
	if err != nil {
		log.Fatalln(err)
	}

	// 打开设备失败不退出，进程内重试。
	//
	// 之前失败就 Fatalln，靠 launchd 重启 —— 但超时那一刻还挂着一个
	// 没法取消的 InitDevice cgo 调用，进程退出等于把它强杀在 coreaudiod
	// 里；"超时→退出→重启"每循环一轮就多攒一个残留，越试越打不开，
	// 最后只能 sudo killall coreaudiod。留在进程里重试，残留至多一个。
	//
	// 每轮先让系统的 say 打开一次设备：macOS 26 上 BlackHole 空置会
	// 回到"未激活"态，非 Apple 进程首开会挂死；say 能唤醒它。
	var player *audio.Player
	for {
		if runtime.GOOS == "darwin" {
			_ = exec.Command("/usr/bin/say", "-a", dev.Name, " ").Run()
		}
		// 解码出来就是立体声交错的 PCM，和 BlackHole 2ch 的格式一致，直接灌进去。
		player, err = actx.NewPlayer(dev, rtc.SampleRate, rtc.Channels, *bufferMS)
		if err == nil {
			break
		}
		log.Println(err)
		log.Println("30 秒后重试打开设备（进程不退出，避免累积驱动残留）")
		time.Sleep(30 * time.Second)
	}
	defer player.Close()

	// 增益补在解码之后、写设备之前。发送端的麦克风音量差异很大，
	// 而下游的语音识别普遍带静音门限 —— 声音送到了但太小，表现和没送到一样。
	ice := iceServers(*stun, *turn, *turnUser, *turnPass)

	level := newLevelMeter()
	agc := audio.NewAGC()
	useAGC := *gain <= 0
	if useAGC {
		log.Println("自动增益已启用")
	} else {
		log.Printf("固定增益 %.2gx", *gain)
	}
	// 诊断录音挂在链条最前面：录的是解码器吐出的原始样本。
	// 波形里若已有硬边缘，病在发送端或传输；若这里干净而听感仍炸，
	// 病在后面的处理和播放。这一刀把整条链切成两半。
	var rec *audio.WAVWriter
	if *record != "" {
		var err error
		if rec, err = audio.NewWAVWriter(*record, rtc.SampleRate, rtc.Channels); err != nil {
			log.Fatalln("打开录音文件失败:", err)
		}
		log.Println("诊断录音:", *record)
	}

	// 分段录音在增益之后：现场要回答的是"刚才那句为什么没识别出来"，
	// 要听的就是识别软件真正听到的那份音频，不是解码器吐出来的原始样本。
	var segs *audio.SegmentRecorder
	var segCh chan []int16
	if *segmentsDir != "" {
		var err error
		if segs, err = audio.NewSegmentRecorder(*segmentsDir, rtc.SampleRate, rtc.Channels); err != nil {
			log.Fatalln("打开分段录音目录失败:", err)
		}
		defer segs.Close()
		log.Println("分段录音:", *segmentsDir)

		// 落盘必须和音频链解耦：SegmentRecorder.Write 是同步磁盘写，
		// 段边界还要建文件、扫目录。直接在解码协程里调，磁盘一卡
		// 播放缓冲就被声卡抽干 —— 为了诊断功能把正事搞出欠载，本末倒置。
		// 队列满就丢帧：录音缺一帧无所谓，音频链一毫秒都不能等。
		segCh = make(chan []int16, 64)
		go func() {
			for pcm := range segCh {
				segs.Write(pcm)
			}
		}()
	}

	// 最近一次往声卡写入有声样本的时刻，环回 watchdog 的因果依据。
	var lastPlayVoiced atomic.Int64
	lastPlayVoiced.Store(time.Now().Unix())

	// 第二路：ring 之后（声卡实际拿到的数据）。和上面那路对比，
	// 缓冲的欠载断口、拉伸、淡入淡出对音质的影响直接能听出来。
	var postSegs *audio.SegmentRecorder
	var postCh chan []int16
	if *segmentsDir != "" {
		var err error
		if postSegs, err = audio.NewSegmentRecorder(filepath.Join(*segmentsDir, "post"), rtc.SampleRate, rtc.Channels); err != nil {
			log.Fatalln("打开 ring 后录音目录失败:", err)
		}
		defer postSegs.Close()
		postCh = make(chan []int16, 64)
		go func() {
			for pcm := range postCh {
				postSegs.Write(pcm)
			}
		}()
		player.SetTap(func(pcm []int16) {
			for _, v := range pcm {
				if v > 500 || v < -500 {
					lastPlayVoiced.Store(time.Now().Unix())
					break
				}
			}
			frame := make([]int16, len(pcm))
			copy(frame, pcm)
			select {
			case postCh <- frame:
			default: // 落盘跟不上就丢帧，绝不拖累声卡回调
			}
		})
	}

	// 第三路：BlackHole 环回之后 —— 识别软件从设备里读到的就是这份。
	// 这里破了"接收端从不打开输入设备"的例，但成不了环：读的是自己
	// 写的 BlackHole，数据只进录音文件，永远不会流回播放缓冲。
	var loopSegs *audio.SegmentRecorder
	if *segmentsDir != "" {
		var err error
		if loopSegs, err = audio.NewSegmentRecorder(filepath.Join(*segmentsDir, "loop"), rtc.SampleRate, rtc.Channels); err != nil {
			log.Fatalln("打开环回录音目录失败:", err)
		}
		defer loopSegs.Close()
		loopCh := make(chan []int16, 64)
		go func() {
			for pcm := range loopCh {
				loopSegs.Write(pcm)
			}
		}()
		capDev, err := actx.FindCapture(*deviceName)
		if err != nil {
			log.Println("环回录音不可用（找不到输入侧设备）:", err)
		} else {
			// 进程快速重启时，CoreAudio 偶尔会发一个坏的 capture 流：
			// 要么回调不来，要么回调照跑、内容却全零。所以判据不能看
			// "有没有回调"，要看因果：我们明明往 BlackHole 写了有声数据
			// （lastPlayVoiced 在 tap 里刷新），环回侧却 60 秒收不到一个
			// 有声样本 —— 环回断了，重开。静音时段两个时间戳都不动，
			// 不会误报。
			var lastLoopVoiced atomic.Int64
			lastLoopVoiced.Store(time.Now().Unix())
			openLoop := func() *audio.Capturer {
				c, err := actx.NewCapturer(capDev, rtc.SampleRate, rtc.Channels, func(pcm []int16) {
					for _, v := range pcm {
						if v > 500 || v < -500 {
							lastLoopVoiced.Store(time.Now().Unix())
							break
						}
					}
					frame := make([]int16, len(pcm))
					copy(frame, pcm)
					select {
					case loopCh <- frame:
					default:
					}
				})
				if err != nil {
					log.Println("环回采集打开失败:", err)
					return nil
				}
				log.Println("环回录音: 从", capDev.Name, "输入侧采集")
				return c
			}
			loopCap := openLoop()
			go func() {
				for range time.Tick(15 * time.Second) {
					now := time.Now().Unix()
					// 没在写有声数据就没有判断依据，静静等着。
					if now-lastPlayVoiced.Load() > 60 {
						continue
					}
					if now-lastLoopVoiced.Load() < 60 {
						continue
					}
					log.Println("环回采集失聪（播放有声而环回 60 秒无声），重开")
					if loopCap != nil {
						loopCap.Close()
					}
					lastLoopVoiced.Store(now) // 重开后重新计时，防连环重开
					loopCap = openLoop()
				}
			}()
		}
	}

	st := &statusState{state: "未连接"}

	receiver := rtc.New(
		func(pcm []int16) {
			if rec != nil {
				rec.Write(pcm)
			}
			if useAGC {
				agc.Process(pcm)
			} else {
				applyGain(pcm, *gain)
			}
			level.observe(pcm)
			player.Write(pcm)
			if segCh != nil {
				frame := make([]int16, len(pcm))
				copy(frame, pcm)
				select {
				case segCh <- frame:
				default: // 落盘跟不上就丢帧，绝不反压音频链
				}
			}
		},
		func(state webrtc.PeerConnectionState) {
			log.Println("连接状态:", state)
			st.setState(state.String())
		},
	)
	receiver.OnPath(func(path string) {
		log.Println("链路:", path)
		st.setPath(path)
	})
	receiver.ExcludeCGNAT(*noCGNAT)
	receiver.SetICEServers(ice)
	receiver.SetDTX(*dtx)
	receiver.ForceRelay(*forceRelay)
	if *forceRelay {
		log.Println("强制中继模式：只接受 TURN 候选")
	}
	if *noCGNAT {
		log.Println("已排除 CGNAT(100.64/10) 候选：强制公网直连")
		if *turn == "" {
			log.Println("警告：没有配 TURN，对称型 NAT 下很可能完全连不上")
		}
	}
	defer receiver.Close()

	// 回传：把公司电脑上的声音送回发送端。
	//
	// 两条路语义差得很远，必须显式选一条：采第二条虚拟线只拿到目标应用的声音；
	// 环回采播放设备会把这台机器上所有系统声音一起送进会议。
	returnSrc, err := resolveReturnSource(*returnDevice, *returnLoopback)
	if err != nil {
		log.Fatalln(err)
	}
	if err := returnSrc.checkNotFeedback(dev.Name); err != nil {
		log.Fatalln(err)
	}
	// 回传电平：和上行一样单独取，用来回答"对面到底有没有声音进来"。
	// 没有这个读数，"听不到会议"只能靠猜。
	var returnLevel *levelMeter
	if returnSrc.enabled() {
		downlink, err := rtc.NewDownlink()
		if err != nil {
			log.Fatalln("建立回传链路失败:", err)
		}
		receiver.SetDownlink(downlink)
		returnLevel = newLevelMeter()
		onReturnPCM := func(pcm []int16) {
			returnLevel.observe(pcm)
			downlink.Write(pcm)
		}

		var captureDev audio.Device
		var capturer *audio.Capturer
		if returnSrc.loopback {
			captureDev, err = actx.FindLoopback(returnSrc.selector)
			if err != nil {
				log.Fatalln(err)
			}
			capturer, err = actx.NewLoopbackCapturer(captureDev, rtc.SampleRate, rtc.Channels, onReturnPCM)
		} else {
			captureDev, err = actx.FindCapture(returnSrc.selector)
			if err != nil {
				log.Fatalln(err)
			}
			capturer, err = actx.NewCapturer(captureDev, rtc.SampleRate, rtc.Channels, onReturnPCM)
		}
		if err != nil {
			log.Fatalln("打开回传采集失败:", err)
		}
		defer capturer.Close()
		if returnSrc.loopback {
			log.Printf("回传: 环回采集 %s（含这台机器的全部系统声音）", captureDev.Name)
		} else {
			log.Printf("回传: 采集 %s", captureDev.Name)
		}
	}
	receiver.OnNotice(func(msg string) { log.Println(msg) })

	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	if name == "" {
		name = "RelayMic 接收端"
	}
	log.Println("机器名:", name)

	// 接收端不监听任何端口：公网入口只有控制面一个。
	// 这台机器在 NAT / 公司防火墙后面也能用，因为整条控制通路都是出站 443。
	if *hub == "" {
		log.Fatalln("缺少 -hub：接收端必须主动连到公网控制面，不再在本机监听端口")
	}
	secret, err := readToken(*token, *tokenFile)
	if err != nil {
		log.Fatalln(err)
	}
	client, err := hubclient.New(hubclient.Config{URL: *hub, Token: secret})
	if err != nil {
		log.Fatalln(err)
	}
	hubCtx, stopHub := context.WithCancel(context.Background())
	defer stopHub()
	go func() {
		err := client.Run(hubCtx, hubclient.Handlers{
			OnCode: func(code string, expiresIn time.Duration) {
				log.Printf("已连接 %s", *hub)
				log.Printf("设备: %s", dev.Name)
				log.Printf("配对码: %s（%d 分钟内有效，用过即换）", code, int(expiresIn.Minutes()))
				st.setState("等待发送端")
			},
			OnJoined: func(session string, sessionICE []signaling.ICEServer) {
				if len(sessionICE) > 0 {
					receiver.SetICEServers(hubICEServers(sessionICE))
					log.Printf("已应用控制面 ICE 配置（%d 项）", len(sessionICE))
				}
				log.Println("发送端已接入")
			},
			OnLeft: func(session string) {
				log.Println("发送端已离开，控制面已换新配对码")
			},
			OnError: func(message string) {
				log.Println("控制面:", message)
			},
			// SDP 只在两端之间走：控制面原样转发，这里也只做编解码转换，
			// 不重新协商、不改写候选。
			OnOffer: func(ctx context.Context, session string, offer json.RawMessage) (json.RawMessage, error) {
				var sdp webrtc.SessionDescription
				if err := json.Unmarshal(offer, &sdp); err != nil {
					return nil, fmt.Errorf("解析 offer: %w", err)
				}
				answer, err := receiver.Answer(sdp)
				if err != nil {
					return nil, err
				}
				return json.Marshal(answer)
			},
		})
		if err != nil {
			log.Fatalln("信令连接失败:", err)
		}
	}()

	mux := http.NewServeMux()

	mux.HandleFunc("/monitor", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(web.MonitorHTML)
	})
	// 缓冲和 RTP 计数现场取：它们本来就带锁，再抄一份到状态里只会多一处会过期的真相。
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		buffered, dropped, starved := player.Stats()
		received, lost, _, _ := receiver.Stats().Snapshot()
		state, path, levelDB, gain := st.snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"state":      state,
			"path":       path,
			"levelDb":    levelDB,
			"gain":       gain,
			"bufferedMs": buffered * 1000 / (rtc.SampleRate * rtc.Channels),
			"dropped":    dropped,
			"starved":    starved,
			"received":   received,
			"lost":       lost,
		})
	})
	toJSON := func(r *audio.SegmentRecorder) []segmentJSON {
		// 页面按数组渲染，没开分段录音时也得给个空数组而不是 null。
		out := []segmentJSON{}
		if r == nil {
			return out
		}
		for _, s := range r.List() {
			out = append(out, segmentJSON{
				Name:   s.Name,
				Time:   s.Time,
				DurMS:  s.DurMS,
				PeakDB: s.PeakDB,
			})
		}
		return out
	}
	mux.HandleFunc("/api/segments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pre":  toJSON(segs),
			"post": toJSON(postSegs),
			"loop": toJSON(loopSegs),
		})
	})
	mux.HandleFunc("GET /api/segments/loop/{name}", func(w http.ResponseWriter, r *http.Request) {
		if loopSegs == nil {
			http.NotFound(w, r)
			return
		}
		name := r.PathValue("name")
		if !validSegmentName(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(loopSegs.Dir(), name))
	})
	mux.HandleFunc("GET /api/segments/post/{name}", func(w http.ResponseWriter, r *http.Request) {
		if postSegs == nil {
			http.NotFound(w, r)
			return
		}
		name := r.PathValue("name")
		if !validSegmentName(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(postSegs.Dir(), name))
	})
	mux.HandleFunc("GET /api/segments/{name}", func(w http.ResponseWriter, r *http.Request) {
		if segs == nil {
			http.NotFound(w, r)
			return
		}
		name := r.PathValue("name")
		// 只放行自己生成的那种文件名。不能只挡 ".."：这个目录在用户家目录下，
		// 拼进任何一个带路径分隔符的名字都等于把整块盘开放给了局域网。
		if !validSegmentName(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(segs.Dir(), name))
	})

	// 本机诊断页是可选的：默认不监听任何端口，要开就自己指定绑到哪。
	// 它只给运维看波形和统计，从不参与信令，因此也不影响"接收端只出站"。
	var monitorSrv *http.Server
	if *monitor != "" {
		monitorSrv = &http.Server{Addr: *monitor, Handler: mux}
		go func() {
			log.Printf("本机诊断页: http://%s/monitor", *monitor)
			if err := monitorSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Println("诊断页已停止:", err)
			}
		}()
	}

	// 每 10 秒报一次缓冲健康度，用来判断要不要调 -buffer。
	go func() {
		lastStarved := 0
		for range time.Tick(10 * time.Second) {
			buffered, dropped, starved := player.Stats()
			recv, lost, reorder, dup := receiver.Stats().Snapshot()
			if recv > 0 {
				log.Printf("缓冲=%d(%.0fms) 丢弃=%d 欠载=%d ｜ RTP 收=%d 丢=%d(%.2f%%) 乱序=%d 重复=%d",
					buffered, float64(buffered)*1000/float64(rtc.SampleRate*rtc.Channels),
					dropped, starved, recv, lost,
					float64(lost)*100/float64(recv+lost), reorder, dup)
				// 欠载又涨了就把见底现场打出来："缓冲够深却见底"这种
				// 矛盾，靠 10 秒采样永远解释不了，只能靠现场数字。
				if starved > lastStarved {
					size, want := player.LastStarve()
					log.Printf("  最近欠载现场：缓冲剩 %d 样本(%.0fms)，声卡要 %d",
						size, float64(size)*1000/float64(rtc.SampleRate*rtc.Channels), want)
				}
				lastStarved = starved
			}
		}
	}()

	// 电平表只有一个消费者：takeDBFS 会清零，监控页和 -meter 各取一次的话，
	// 两边都只能看到半截读数。这里统一取，再决定要不要打日志。
	go func() {
		for range time.Tick(time.Second) {
			g := *gain
			if useAGC {
				g = agc.Gain()
			}
			db, ok := level.takeDBFS()
			if !ok {
				// 这一秒一个样本都没来，等同于静音，否则页面会一直挂着上一次的读数。
				st.setLevel(-120, g)
			} else {
				st.setLevel(db, g)
			}
			if returnLevel != nil {
				// 回传的电平每秒都要取走，否则峰值会一直累加到下一次有人看。
				if rdb, rok := returnLevel.takeDBFS(); rok && *meter {
					log.Printf("回传 %6.1f dBFS %s", rdb, bar(rdb))
				}
			}
			if !*meter || !ok {
				continue
			}
			if useAGC {
				log.Printf("电平 %6.1f dBFS %s  增益 %.1fx", db, bar(db), agc.Gain())
			} else {
				log.Printf("电平 %6.1f dBFS %s", db, bar(db))
			}
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("退出")
	stopHub()
	if monitorSrv != nil {
		_ = monitorSrv.Close()
	}
}

// statusState 是 /api/status 里那几个"只有回调和定时器知道"的值。
// 写它的是 ICE 回调和电平协程，读它的是 HTTP 处理器，全在不同的协程上。
type statusState struct {
	mu      sync.Mutex
	state   string
	path    string
	levelDB float64
	gain    float64
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

func (s *statusState) setLevel(db, gain float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.levelDB, s.gain = db, gain
}

func (s *statusState) snapshot() (state, path string, levelDB, gain float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.path, s.levelDB, s.gain
}

// segmentJSON 是 audio.SegmentInfo 的传输形态，字段名对齐监控页。
type segmentJSON struct {
	Name   string    `json:"name"`
	Time   time.Time `json:"time"`
	DurMS  int       `json:"durMs"`
	PeakDB float64   `json:"peakDb"`
}

// validSegmentName 只认分段录音自己生成的文件名："20060102-150405[-N].wav"。
// 白名单而非黑名单：够用，而且不必去想还有哪几种写法能绕过。
func validSegmentName(name string) bool {
	base, ok := strings.CutSuffix(name, ".wav")
	if !ok || base == "" {
		return false
	}
	for _, c := range base {
		if (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
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
