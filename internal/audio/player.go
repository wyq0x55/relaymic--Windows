package audio

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

// Player 把 PCM 持续写进一个输出设备。
//
// 网络来的音频是突发的，声卡的取数是匀速的，中间必须有一个缓冲垫着。
// 这里的取舍按语音场景来：宁可多缓冲一点延迟，也不要断字。
type Player struct {
	device *malgo.Device
	ring   *ring
}

// OpenTimeout 是打开音频设备的等待上限。
//
// CoreAudio 的设备初始化可能永久挂起 —— 例如某个持有该设备的进程被强杀，
// 驱动里残留了没释放的实例。这台机器在异地，没人能去点一下，
// 所以宁可退出让 launchd 重启，也不能无声地卡在那里。
const OpenTimeout = 10 * time.Second

// NewPlayer 在 dev 上开一个播放流。
// sampleRate/channels 必须和喂进 Write 的 PCM 一致。
// targetMS 是抖动缓冲的目标深度。
func (c *Context) NewPlayer(dev Device, sampleRate, channels, targetMS int) (*Player, error) {
	// 缓冲上限取目标的 4 倍：网络抖动时能吸收，但不会让延迟无限堆积。
	capacity := sampleRate * channels * targetMS * 4 / 1000

	p := &Player{ring: newRing(capacity, sampleRate*channels*targetMS/1000, channels)}

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = uint32(channels)
	cfg.Playback.DeviceID = dev.ID.Pointer()
	cfg.SampleRate = uint32(sampleRate)

	type result struct {
		device *malgo.Device
		err    error
	}
	// InitDevice 是阻塞的 cgo 调用，没法取消；超时后只能弃用这个协程，
	// 由上层退出进程来收拾。这里至少保证调用方不会永远等下去。
	done := make(chan result, 1)
	go func() {
		device, err := malgo.InitDevice(c.ctx.Context, cfg, malgo.DeviceCallbacks{
			Data: func(out, _ []byte, frameCount uint32) {
				p.ring.readInto(out, int(frameCount)*channels)
			},
		})
		done <- result{device, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("打开输出设备 %q: %w", dev.Name, r.err)
		}
		if err := r.device.Start(); err != nil {
			r.device.Uninit()
			return nil, fmt.Errorf("启动输出设备 %q: %w", dev.Name, err)
		}
		p.device = r.device
		return p, nil
	case <-time.After(OpenTimeout):
		return nil, fmt.Errorf("打开输出设备 %q 超过 %s 无响应；"+
			"通常是驱动里残留了未释放的实例，在那台机器上执行 sudo killall coreaudiod 可恢复",
			dev.Name, OpenTimeout)
	}
}

// Write 把一帧 PCM 交给播放缓冲。不阻塞：缓冲满了会丢最旧的数据。
func (p *Player) Write(pcm []int16) {
	p.ring.write(pcm)
}

// Stats 返回当前缓冲深度（样本数）和累计丢弃数，用于观察连接质量。
func (p *Player) Stats() (buffered, dropped, starved int) {
	return p.ring.stats()
}

// SetTap 注册输出旁路回调。fn 在声卡实时回调里被调：
// 只许拷走数据立即返回，不许阻塞。传入的切片用完即弃。
func (p *Player) SetTap(fn func([]int16)) {
	p.ring.mu.Lock()
	defer p.ring.mu.Unlock()
	p.ring.tapFn = fn
}

// LastStarve 返回最近一次欠载的现场：见底时剩多少、声卡要多少。
func (p *Player) LastStarve() (size, want int) {
	p.ring.mu.Lock()
	defer p.ring.mu.Unlock()
	return p.ring.starveSize, p.ring.starveWant
}

func (p *Player) Close() {
	if p.device != nil {
		p.device.Uninit()
		p.device = nil
	}
}

// ring 是一个定长的 int16 环形缓冲。
//
// 写入来自解码协程，读取来自声卡的实时回调 —— 回调里绝不能阻塞或分配，
// 所以这里只用一把 mutex 和预分配的 slice。
type ring struct {
	mu   sync.Mutex
	buf  []int16
	head int // 下一个读位置
	size int // 当前有效样本数

	prefill int  // 起播前至少攒够这么多样本
	filling bool // 是否处于攒数据阶段
	fed     bool // 上次判空以来有没有新数据进来，用于区分欠载和源头静默

	// 有声和无声之间不能硬切。波形从任意值瞬间跳到零（或反过来）
	// 是一次宽频脉冲，听感就是短促的"咔哒/biu"—— 发送端开着 DTX 时
	// 每句话的首尾都有这种边界，不处理的话每次停顿都响一声。
	channels int
	tail     []float64 // 每声道最后送出的值，静音期从这里衰减到零
	rampIn   int       // 恢复出声后剩余的淡入帧数
	quiet    bool      // 上一次回调是否在出静音，用于触发淡入

	stretchCnt int // 拉伸模式的帧计数器

	// 输出旁路：把 readInto 实际交给声卡的样本原样再给一份出去，
	// 用于录"ring 之后"的音频。欠载断口、拉伸、淡入淡出全都体现在
	// 这份数据里 —— 和 ring 之前那份对比，缓冲对音质的影响一听便知。
	tapFn  func([]int16)
	tapBuf []int16

	dropped int
	starved int
	// 最近一次欠载的现场。10 秒一次的统计只能看到"欠载了"，
	// 看不到见底那一刻缓冲里到底剩多少 —— 而"缓冲明明够深却见底"
	// 这种矛盾，只有现场数字能解释。
	starveSize int // 见底时缓冲剩余样本
	starveWant int // 声卡当时要多少
}

// 淡入淡出的时长（帧数）。48kHz 下 96 帧 = 2ms：足以压掉脉冲，
// 短到不会吃掉字头。
const rampFrames = 96

// 拉伸模式下每多少帧重复一帧（0.5% 变速）。
const stretchEvery = 200

func newRing(capacity, prefill, channels int) *ring {
	if capacity < 1 {
		capacity = 1
	}
	if channels < 1 {
		channels = 1
	}
	return &ring{
		buf:      make([]int16, capacity),
		prefill:  prefill,
		filling:  true,
		channels: channels,
		tail:     make([]float64, channels),
		quiet:    true,
	}
}

func (r *ring) write(pcm []int16) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range pcm {
		if r.size == len(r.buf) {
			// 满了：丢掉最旧的一个样本给新数据让位。
			r.head = (r.head + 1) % len(r.buf)
			r.size--
			r.dropped++
		}
		r.buf[(r.head+r.size)%len(r.buf)] = s
		r.size++
	}

	r.fed = true

	if r.filling && r.size >= r.prefill {
		r.filling = false
	}

	// 缓冲长期高于目标，就是延迟在白白堆积：发送端时钟和本机声卡时钟
	// 差百万分之几，几分钟就能攒出几百毫秒。
	//
	// 但收缩必须是"渗"出去的，不能"砍"。一次性丢掉超出的部分意味着
	// 瞬间跳过上百毫秒音频 —— 听感上就是明显的跳字，比多几百毫秒延迟糟得多。
	//
	// 所以每次按超出量的一小比例收，堆得越多收得越快，但单次上限锁死在
	// 目标的十分之一：既跟得上漂移，又不会一次跳掉一个字。
	if limit := r.prefill * 2; limit > 0 && r.size > limit {
		bite := (r.size - r.prefill) / 8
		if maxBite := r.prefill / 10; bite > maxBite {
			bite = maxBite
		}
		if bite < 1 {
			bite = 1
		}
		if bite > r.size {
			bite = r.size
		}
		r.head = (r.head + bite) % len(r.buf)
		r.size -= bite
		r.dropped += bite
	}
}

// readInto 填满 out（S16 小端），不够的部分出衰减尾音而非硬切到零。
func (r *ring) readInto(out []byte, samples int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 还在攒数据，或者刚被掏空：先出静音，等缓冲重新垫起来再放。
	// 直接放不完整的数据会听成断字，静音反而更容易被识别器忽略。
	if r.filling {
		r.fadeOut(out, samples)
		return
	}
	if r.size < samples {
		// 缓冲见底有两种成因，只有一种是故障：
		//
		//   数据还在源源不断地来，却接不上 —— 真欠载，缓冲太浅或网络在抖；
		//   源头压根停了 —— 发送端开着 DTX，静音期本来就不发包。
		//
		// 后者每次说话停顿都会发生，而声卡回调约每 10ms 就来一次：停两秒
		// 就能记上两百次。全算进欠载，这个数字就再也说明不了任何问题，
		// "欠载频繁就调大 -buffer" 那条判据也跟着失效。
		//
		// 所以只在"喂过新数据之后又见底"时记一次。断线同理：算一次，
		// 而不是在整段掉线期间无限累加。
		if r.fed {
			r.starved++
			r.starveSize = r.size
			r.starveWant = samples
		}
		r.fed = false
		r.filling = true // 重新攒够 prefill 再起播
		r.fadeOut(out, samples)
		return
	}

	// 刚从静音里出来：淡入起播，压掉从零跳进波形中段的脉冲。
	if r.quiet {
		r.rampIn = rampFrames
		r.quiet = false
	}

	// 供给追不上消耗时（两端时钟没对齐，实测缓冲会以每分钟上百毫秒的
	// 速度漏干，然后见底断一次），轻微拖时间：每 stretchEvery 帧重复
	// 输出一帧，消耗速率降 0.5%，缓冲自己爬回去。0.5% 的变速人耳听不出，
	// 150ms 的断口谁都听得出。
	stretch := r.size < r.prefill/2

	frames := samples / r.channels
	for f := 0; f < frames; f++ {
		repeat := false
		if stretch {
			r.stretchCnt++
			if r.stretchCnt >= stretchEvery {
				r.stretchCnt = 0
				repeat = true
			}
		}
		for c := 0; c < r.channels; c++ {
			v := float64(r.buf[(r.head+c)%len(r.buf)])
			if r.rampIn > 0 {
				v *= float64(rampFrames-r.rampIn) / rampFrames
			}
			r.tail[c] = v
			binary.LittleEndian.PutUint16(out[(f*r.channels+c)*2:], uint16(int16(v)))
		}
		if r.rampIn > 0 {
			r.rampIn--
		}
		if !repeat {
			r.head = (r.head + r.channels) % len(r.buf)
			r.size -= r.channels
		}
	}
	r.tap(out, samples)
}

// tap 把刚交给声卡的字节转回样本给旁路。缓冲复用，不在实时回调里反复分配。
func (r *ring) tap(out []byte, samples int) {
	if r.tapFn == nil {
		return
	}
	if cap(r.tapBuf) < samples {
		r.tapBuf = make([]int16, samples)
	}
	r.tapBuf = r.tapBuf[:samples]
	for i := 0; i < samples; i++ {
		r.tapBuf[i] = int16(binary.LittleEndian.Uint16(out[i*2:]))
	}
	r.tapFn(r.tapBuf)
}

// fadeOut 从最后送出的样本值指数衰减到零，几毫秒内归于安静。
// 起点接着上一次的出声，所以边界处没有跳变。
func (r *ring) fadeOut(out []byte, samples int) {
	r.quiet = true
	for i := 0; i < samples; i++ {
		ch := i % r.channels
		r.tail[ch] *= 0.9
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(r.tail[ch])))
	}
	r.tap(out, samples)
}

func (r *ring) stats() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size, r.dropped, r.starved
}
