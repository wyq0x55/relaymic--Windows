package rtc

import (
	"strings"
	"sync"
	"testing"

	"github.com/pion/webrtc/v4"
)

type noticeLog struct {
	mu   sync.Mutex
	msgs []string
}

func (n *noticeLog) add(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, msg)
}

func (n *noticeLog) all() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.msgs, "\n")
}

// twoWayOffer 造一个双向 offer：一条只发（浏览器推麦克风），
// 一条只收（浏览器要听公司电脑的声音）。这正是新版页面的形状。
func twoWayOffer(t *testing.T) webrtc.SessionDescription {
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
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("添加接收通道: %v", err)
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

func newTestReceiver(t *testing.T, log *noticeLog) *Receiver {
	t.Helper()
	r := New(nil, nil)
	r.SetICEServers(nil) // 不查 STUN，只收 host 候选：测试不该依赖外网
	if log != nil {
		r.OnNotice(log.add)
	}
	t.Cleanup(r.Close)
	return r
}

func TestAnswerSendsAudioWhenTheSenderAsksForIt(t *testing.T) {
	log := &noticeLog{}
	r := newTestReceiver(t, log)
	d, err := NewDownlink()
	if err != nil {
		t.Fatalf("NewDownlink() error = %v", err)
	}
	r.SetDownlink(d)

	answer, err := r.Answer(twoWayOffer(t))
	if err != nil {
		t.Fatalf("协商失败: %v", err)
	}
	if got := strings.Count(answer.SDP, "m=audio"); got != 2 {
		t.Fatalf("answer 里有 %d 条音频通道，期望 2\n%s", got, answer.SDP)
	}
	if !strings.Contains(answer.SDP, "a=sendonly") {
		t.Fatalf("answer 没有声明发送方向，浏览器不会收到声音\n%s", answer.SDP)
	}
	if got := log.all(); got != "" {
		t.Fatalf("协商过程中出现不该有的提示: %s", got)
	}
}

// 对端要收、我们却没开回传时必须如实回答 inactive：
// 声明了自己不会发的方向，浏览器会一直等一个永远不来的音轨。
func TestAnswerStaysInactiveWhenNoReturnTrackIsConfigured(t *testing.T) {
	r := newTestReceiver(t, nil)
	answer, err := r.Answer(twoWayOffer(t))
	if err != nil {
		t.Fatalf("协商失败: %v", err)
	}
	if got := strings.Count(answer.SDP, "m=audio"); got != 2 {
		t.Fatalf("answer 里有 %d 条音频通道，期望 2\n%s", got, answer.SDP)
	}
	if strings.Contains(answer.SDP, "a=sendonly") {
		t.Fatalf("没有回传音轨却声明了发送方向\n%s", answer.SDP)
	}
}

// 页面还是旧版（只声明发送）时，回传挂不上去。
// 这时必须降级成"只上行"并说清楚，而不是把整条上行也一起谈崩。
func TestAnswerDegradesToUplinkOnlyWhenTheSenderCannotReceive(t *testing.T) {
	log := &noticeLog{}
	r := newTestReceiver(t, log)
	d, err := NewDownlink()
	if err != nil {
		t.Fatalf("NewDownlink() error = %v", err)
	}
	r.SetDownlink(d)

	answer, err := r.Answer(senderOffer(t))
	if err != nil {
		t.Fatalf("协商失败: %v", err)
	}
	if got := strings.Count(answer.SDP, "m=audio"); got != 1 {
		t.Fatalf("answer 里有 %d 条音频通道，期望 1\n%s", got, answer.SDP)
	}
	notice := log.all()
	if !strings.Contains(notice, "只上行") {
		t.Fatalf("没有告诉运维回传被跳过了，日志是 %q", notice)
	}
}
