// selfcheck 冒充一个发送端，把已知的测试音推给接收端。
//
// 它存在的理由是：出问题时要能一句话回答"是网络坏了还是音频坏了"。
// 浏览器那条路要人工授权麦克风，没法用来做回归验证，这个可以。
package selfcheck

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	sampleRate = 48000
	// SDP 里的 Opus 恒定是 opus/48000/2，声道数必须和接收端协商一致。
	channels    = 2
	frameMS     = 20
	frameSize   = sampleRate / 1000 * frameMS // 每帧每声道样本数
	toneHz      = 440
	toneAmpl    = 8000 // int16 满量程的约 1/4，对应 -12dB
	maxOpusSize = 4000
)

// Main 以命令行方式运行 selfcheck。args 是命令名之后的参数。
func Main(args []string) int {
	os.Args = append([]string{"relaymic selfcheck"}, args...)
	run()
	return 0
}

func run() {
	target := flag.String("target", "https://localhost:7420", "接收端地址")
	duration := flag.Duration("duration", 8*time.Second, "发送时长")
	turnURL := flag.String("turn", "", "TURN 地址")
	turnUser := flag.String("turn-user", "", "TURN 用户名")
	turnPass := flag.String("turn-pass", "", "TURN 密码")
	forceRelay := flag.Bool("force-relay", false, "只用 TURN 中继候选")
	flag.Parse()

	log.SetFlags(log.Ltime)

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: sampleRate,
			Channels:  channels,
		}, "audio", "selfcheck")
	if err != nil {
		die(err)
	}

	cfg := webrtc.Configuration{}
	if *turnURL != "" {
		cfg.ICEServers = []webrtc.ICEServer{{
			URLs:       []string{*turnURL},
			Username:   *turnUser,
			Credential: *turnPass,
		}}
	}
	if *forceRelay {
		cfg.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		die(err)
	}
	defer pc.Close()

	sender, err := pc.AddTrack(track)
	if err != nil {
		die(err)
	}
	// 不读走 RTCP 的话，反馈包会堆在缓冲里。
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	connected := make(chan struct{})
	var once bool
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Println("连接状态:", s)
		if s == webrtc.PeerConnectionStateConnected && !once {
			once = true
			close(connected)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		die(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		die(err)
	}
	<-gathered

	answer, err := negotiate(*target, pc.LocalDescription())
	if err != nil {
		die(err)
	}
	if err := pc.SetRemoteDescription(*answer); err != nil {
		die(err)
	}

	// 走 TURN 时要先分配中继再做连通性检查，10 秒不够。
	connectTimeout := 30 * time.Second
	select {
	case <-connected:
	case <-time.After(connectTimeout):
		die(fmt.Errorf("%s 内没有连上 %s", connectTimeout, *target))
	}

	log.Printf("发送 %dHz 测试音 %s", toneHz, *duration)
	if err := sendTone(track, *duration); err != nil {
		die(err)
	}
	log.Println("完成")
}

func negotiate(target string, offer *webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	body, err := json.Marshal(offer)
	if err != nil {
		return nil, err
	}
	// 接收端用的是自签证书，自检工具没必要为此配信任链。
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := client.Post(target+"/offer", "application/json", bytes.NewReader(body))
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

func sendTone(track *webrtc.TrackLocalStaticSample, duration time.Duration) error {
	enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
	if err != nil {
		return fmt.Errorf("创建 Opus 编码器: %w", err)
	}

	pcm := make([]int16, frameSize*channels)
	buf := make([]byte, maxOpusSize)
	phase := 0.0
	step := 2 * math.Pi * toneHz / sampleRate

	ticker := time.NewTicker(frameMS * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(duration)

	for range ticker.C {
		if time.Now().After(deadline) {
			return nil
		}
		for i := 0; i < frameSize; i++ {
			v := int16(math.Sin(phase) * toneAmpl)
			for c := 0; c < channels; c++ {
				pcm[i*channels+c] = v
			}
			phase += step
		}
		n, err := enc.Encode(pcm, buf)
		if err != nil {
			return fmt.Errorf("Opus 编码: %w", err)
		}
		if err := track.WriteSample(media.Sample{
			Data:     buf[:n],
			Duration: frameMS * time.Millisecond,
		}); err != nil {
			return fmt.Errorf("写入轨道: %w", err)
		}
	}
	return nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
