package rtc

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	// frameDuration 是回传的 Opus 帧长。20ms 是 WebRTC 的通用节奏：
	// 再短包头开销占比高，再长抗丢包变差。
	frameDuration = 20 * time.Millisecond
	// frameValues 是一帧里 int16 样本的个数（48kHz 交错立体声）。
	frameValues = SampleRate / 1000 * 20 * Channels
	// maxPayloadBytes 是 Opus 一帧的编码缓冲上限。2 声道 20ms 即使跑满
	// 510kbps 也只有 1275 字节左右，留三倍余量。
	maxPayloadBytes = 4096
)

// sampleWriter 是 WriteSample 的最小依赖面：测试用假实现就能覆盖分帧与编码，
// 不必先建起一条 PeerConnection。
type sampleWriter interface {
	WriteSample(media.Sample) error
}

// Downlink 把公司电脑上的声音编码成 Opus，写进一条只发不收的音轨。
//
// 它是上行链路的镜像：那边把远端的 PCM 解码后写进虚拟线，
// 这边采虚拟线（或环回）编码后送回远端。音频仍然只在两端之间走。
type Downlink struct {
	w     sampleWriter
	track *webrtc.TrackLocalStaticSample
	enc   *opus.Encoder

	mu      sync.Mutex
	buf     []int16
	payload []byte
}

// NewDownlink 建一条 Opus 48kHz 立体声的回传音轨。
func NewDownlink() (*Downlink, error) {
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: SampleRate,
			Channels:  Channels,
		},
		"relaymic-return", "relaymic",
	)
	if err != nil {
		return nil, fmt.Errorf("创建回传音轨: %w", err)
	}
	d, err := newDownlinkWith(track)
	if err != nil {
		return nil, err
	}
	d.track = track
	return d, nil
}

func newDownlinkWith(w sampleWriter) (*Downlink, error) {
	enc, err := opus.NewEncoder(SampleRate, Channels, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("创建 Opus 编码器: %w", err)
	}
	// 和上行同样的理由：语音清晰度优先，这条链路带宽不是瓶颈。
	if err := enc.SetBitrate(96000); err != nil {
		return nil, fmt.Errorf("设置 Opus 码率: %w", err)
	}
	// 带内 FEC 把前一帧的低码率副本捎在下一个包里。丢一个包就是丢一个音节，
	// 而重传要等一个 RTT —— 等到了也过了该播的时刻。
	if err := enc.SetInBandFEC(true); err != nil {
		return nil, fmt.Errorf("开启 Opus 带内 FEC: %w", err)
	}
	if err := enc.SetPacketLossPerc(10); err != nil {
		return nil, fmt.Errorf("设置 Opus 期望丢包率: %w", err)
	}
	return &Downlink{
		w:       w,
		enc:     enc,
		buf:     make([]int16, 0, frameValues),
		payload: make([]byte, maxPayloadBytes),
	}, nil
}

// Track 是挂到 PeerConnection 上的那条音轨。
func (d *Downlink) Track() *webrtc.TrackLocalStaticSample { return d.track }

// Write 吃采集回调给的 PCM（48kHz 交错立体声），攒够一帧就编码发出去。
//
// 回调给的块大小由驱动决定，通常不是 20ms 的整数倍，所以必须自己缓冲；
// 但只缓存不满一帧的尾巴 —— 多存一帧就是白加 20ms 延迟。
func (d *Downlink) Write(pcm []int16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for len(pcm) > 0 {
		take := frameValues - len(d.buf)
		if take > len(pcm) {
			take = len(pcm)
		}
		d.buf = append(d.buf, pcm[:take]...)
		pcm = pcm[take:]
		if len(d.buf) < frameValues {
			continue
		}
		d.emitLocked()
		d.buf = d.buf[:0]
	}
}

func (d *Downlink) emitLocked() {
	n, err := d.enc.Encode(d.buf, d.payload)
	if err != nil || n <= 0 {
		// 编码失败就丢这一帧。回传少一帧是听感问题，
		// 阻塞采集回调是整条链路的问题，两者不能互换。
		return
	}
	// 交给包化器的是独立内存：pion 的拦截器（NACK 等）会持有已发包的引用，
	// 复用 d.payload 会让重传发出被后续帧覆盖过的内容。
	frame := make([]byte, n)
	copy(frame, d.payload[:n])
	_ = d.w.WriteSample(media.Sample{Data: frame, Duration: frameDuration})
}

// offerHasReceiveOnlyAudio 判断 offer 里有没有一条专门用来收音频的 m-line。
//
// 需要它是因为回传通道必须由我们自己建：pion 为远端 m-line 自动建出来的收发器，
// 在 CreateAnswer 前后 Sender() 都是 nil（实测 v4.2.18），音轨挂不上去。
// 而自己多建一条又会给只推流的旧页面凭空加一条 m-line，所以要先看清楚对端要什么。
//
// 只认 a=recvonly，不认 a=sendrecv：sendrecv 那条 m-line 的接收方向本来就和
// 发送方向挤在一起，挂回传要改写它的协商方向，风险远大于收益。我们自己的页面
// 用的是显式的 recvonly 通道，旧页面是显式的 sendonly —— 两边都判得准。
//
// 按 m= 分段扫描，只看音频段里的方向行 —— 免得把视频段的方向算到音频头上。
func offerHasReceiveOnlyAudio(sdp string) bool {
	for _, section := range strings.Split(sdp, "m=") {
		if !strings.HasPrefix(section, "audio ") {
			continue
		}
		for _, line := range strings.Split(section, "\r\n") {
			line = strings.TrimSpace(line)
			if line == "a=recvonly" {
				return true
			}
			// 方向行只出现一次，遇到任何一条就可以停止扫这一段。
			if line == "a=sendonly" || line == "a=sendrecv" || line == "a=inactive" {
				break
			}
		}
	}
	return false
}
