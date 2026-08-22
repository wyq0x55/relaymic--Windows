// receiver 跑在被远程控制的那台 Mac 上。
//
// 它做三件事：托管发送端网页、收 WebRTC 音频、把音频写进虚拟麦克风。
// 用户在任何吃麦克风的软件里选中那个虚拟设备，就能听到本地说的话。
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"log"
	"math"
	"net"
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
	"github.com/hueshu/relaymic/internal/discover"
	"github.com/hueshu/relaymic/internal/rtc"
	"github.com/hueshu/relaymic/internal/tlscert"
	"github.com/hueshu/relaymic/internal/web"
	"github.com/pion/webrtc/v4"
)

func defaultCertDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".remotemic"
	}
	return filepath.Join(home, ".config", "remotemic")
}

func defaultSegmentsDir() string { return filepath.Join(defaultCertDir(), "recordings") }

func main() {
	addr := flag.String("addr", ":7420", "监听地址")
	deviceName := flag.String("device", "blackhole", "输出设备名（子串匹配）")
	// 150ms 是实测值：80ms 扛不住 WiFi 突发，600ms 白垫延迟。
	bufferMS := flag.Int("buffer", 150, "抖动缓冲目标深度（毫秒）")
	plain := flag.Bool("plain", false, "用 http 而非 https（只有从本机访问才够用）")
	certDir := flag.String("cert-dir", defaultCertDir(), "自签证书存放目录")
	certHosts := flag.String("cert-hosts", "", "额外写进证书的域名或 IP，逗号分隔")
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

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(web.FS())))
	// 发送端必须用同一套 ICE 配置：只有它也拿到 TURN，
	// 才会生成中继候选，ICE 才可能选中那条低延迟的路。
	// 机器名随 ICE 配置一起给发送端：网页/界面上"IP 旁边是哪台电脑"
	// 由每台自己回答。优先用 Tailscale 里设定的设备名（用户按它管理机器，
	// 显示别的名字对不上号），退而求其次才是系统的电脑名。
	name := discover.SelfName()
	if name != "" {
		log.Println("机器名(来自 Tailscale):", name)
	}
	if name == "" {
		if out, err := exec.Command("/usr/sbin/scutil", "--get", "ComputerName").Output(); err == nil {
			name = strings.TrimSpace(string(out))
		}
	}
	if name == "" {
		name, _ = os.Hostname()
		name = strings.TrimSuffix(name, ".local")
	}
	if name != "" {
		log.Println("机器名:", name)
	}
	mux.HandleFunc("/ice-config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"iceServers":   ice,
			"excludeCGNAT": *noCGNAT,
			"name":         name,
		})
	})
	mux.HandleFunc("/offer", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
			return
		}
		var offer webrtc.SessionDescription
		if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
			http.Error(w, "offer 解析失败: "+err.Error(), http.StatusBadRequest)
			return
		}
		answer, err := receiver.Answer(offer)
		if err != nil {
			log.Println("协商失败:", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Println("发送端已接入", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer)
	})

	// 替浏览器发现接收端：网页拿不到 tailscale 设备表，这台替它扫。
	// 结果含扫描者自己拿不到的"其他台"；网页把自己的 origin 合并进去。
	var rcvMu sync.Mutex
	var rcvCache []string
	var rcvAt time.Time
	mux.HandleFunc("/api/receivers", func(w http.ResponseWriter, r *http.Request) {
		rcvMu.Lock()
		if time.Since(rcvAt) > 30*time.Second {
			found, err := discover.Receivers()
			if err != nil {
				log.Println("接收端扫描失败:", err)
			} else {
				rcvCache = found
				rcvAt = time.Now()
			}
		}
		out := rcvCache
		rcvMu.Unlock()
		if out == nil {
			out = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

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

	// 网页发送端从一台打开、同时连所有台：跨源请求必须放行。
	// 局域网/tailnet 自用服务，没有共享凭据，通配符是安全的。
	cors := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})

	srv := &http.Server{Addr: *addr, Handler: cors}

	_, port, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalln("解析监听地址:", err)
	}
	ips := localIPv4()

	scheme := "http"
	if !*plain {
		// 浏览器只在安全上下文里给麦克风权限，所以除了 localhost，
		// 发送端必须走 https —— 这不是可选项，是能不能用的前提。
		hosts := append([]string{"localhost", "127.0.0.1"}, ips...)
		if *certHosts != "" {
			hosts = append(hosts, strings.Split(*certHosts, ",")...)
		}
		cert, err := tlscert.Ensure(*certDir, hosts)
		if err != nil {
			log.Fatalln("准备证书失败:", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		scheme = "https"
	}

	go func() {
		log.Printf("虚拟麦克风: %s", dev.Name)
		log.Printf("发送端地址: %s://localhost:%s", scheme, port)
		log.Printf("监控页面: %s://localhost:%s/monitor", scheme, port)
		for _, ip := range ips {
			log.Printf("发送端地址: %s://%s:%s", scheme, ip, port)
			log.Printf("监控页面: %s://%s:%s/monitor", scheme, ip, port)
		}
		if scheme == "https" {
			log.Println("自签证书：浏览器首次会警告，点「高级 → 继续前往」，之后会被记住")
		}

		var err error
		if *plain {
			err = srv.ListenAndServe()
		} else {
			err = srv.ListenAndServeTLS("", "")
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalln(err)
		}
	}()

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
				continue
			}
			st.setLevel(db, g)
			if !*meter {
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
	_ = srv.Close()
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

// localIPv4 列出本机对外可达的 IPv4，用来生成证书 SAN 和提示访问地址。
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
