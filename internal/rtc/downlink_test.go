package rtc

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// fakeWriter 记下每一帧，用来在没有 PeerConnection 的情况下验分帧与编码。
type fakeWriter struct {
	mu      sync.Mutex
	samples []media.Sample
}

func (f *fakeWriter) WriteSample(s media.Sample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = append(f.samples, media.Sample{
		Data:     append([]byte(nil), s.Data...),
		Duration: s.Duration,
	})
	return nil
}

func (f *fakeWriter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.samples)
}

func (f *fakeWriter) first() media.Sample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.samples[0]
}

func newTestDownlink(t *testing.T) (*Downlink, *fakeWriter) {
	t.Helper()
	w := &fakeWriter{}
	d, err := newDownlinkWith(w)
	if err != nil {
		t.Fatalf("newDownlinkWith() error = %v", err)
	}
	return d, w
}

func tone(values int) []int16 {
	pcm := make([]int16, values)
	for i := range pcm {
		pcm[i] = int16(math.Sin(float64(i)/8.0) * 12000)
	}
	return pcm
}

func TestNewDownlinkUsesOpus48kStereo(t *testing.T) {
	d, err := NewDownlink()
	if err != nil {
		t.Fatalf("NewDownlink() error = %v", err)
	}
	codec := d.Track().Codec()
	if codec.MimeType != webrtc.MimeTypeOpus {
		t.Fatalf("mime = %q, want %q", codec.MimeType, webrtc.MimeTypeOpus)
	}
	if codec.ClockRate != SampleRate {
		t.Fatalf("clock rate = %d, want %d", codec.ClockRate, SampleRate)
	}
	if codec.Channels != Channels {
		t.Fatalf("channels = %d, want %d", codec.Channels, Channels)
	}
}

func TestWriteEmitsOneSamplePer20msFrame(t *testing.T) {
	d, w := newTestDownlink(t)
	d.Write(tone(frameValues))

	if got := w.count(); got != 1 {
		t.Fatalf("emitted %d samples, want 1", got)
	}
	sample := w.first()
	if sample.Duration != frameDuration {
		t.Fatalf("duration = %v, want %v", sample.Duration, frameDuration)
	}
	if len(sample.Data) == 0 {
		t.Fatal("payload is empty")
	}
	if len(sample.Data) > maxPayloadBytes {
		t.Fatalf("payload %d bytes exceeds the %d byte buffer", len(sample.Data), maxPayloadBytes)
	}
}

// 采集回调给的块大小是驱动决定的，不可能正好是 20ms。
// 分帧必须自己缓冲，不能假设每次 Write 就是一帧。
func TestWriteBuffersPartialFrames(t *testing.T) {
	d, w := newTestDownlink(t)
	half := frameValues / 2

	d.Write(tone(half))
	if got := w.count(); got != 0 {
		t.Fatalf("emitted %d samples after half a frame, want 0", got)
	}
	d.Write(tone(half))
	if got := w.count(); got != 1 {
		t.Fatalf("emitted %d samples after a full frame, want 1", got)
	}
}

func TestWriteIgnoresEmptyInput(t *testing.T) {
	d, w := newTestDownlink(t)
	d.Write(nil)
	d.Write([]int16{})
	if got := w.count(); got != 0 {
		t.Fatalf("emitted %d samples for empty input, want 0", got)
	}
}

// 任意切分方式都要得到同样的帧数：奇数值的块会把立体声对切在中间，
// 缓冲逻辑必须对"半对样本"也成立。
func TestWriteHandlesArbitraryChunkSizes(t *testing.T) {
	d, w := newTestDownlink(t)
	const fullFrames = 3
	total := frameValues*fullFrames + 37
	pcm := tone(total)

	for i := 0; i < total; i += 101 {
		end := i + 101
		if end > total {
			end = total
		}
		d.Write(pcm[i:end])
	}
	if got := w.count(); got != fullFrames {
		t.Fatalf("emitted %d samples, want %d", got, fullFrames)
	}
}

func TestWriteIsSafeForConcurrentUse(t *testing.T) {
	d, w := newTestDownlink(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				d.Write(tone(480))
			}
		}()
	}
	wg.Wait()
	// 4 个协程各写 50×480=24000 个值，共 96000，正好 50 帧。
	if got := w.count(); got != 50 {
		t.Fatalf("emitted %d samples, want 50", got)
	}
}

func TestFrameConstantsMatchTheOpusContract(t *testing.T) {
	if frameDuration != 20*time.Millisecond {
		t.Fatalf("frameDuration = %v, want 20ms", frameDuration)
	}
	if want := SampleRate / 1000 * 20 * Channels; frameValues != want {
		t.Fatalf("frameValues = %d, want %d", frameValues, want)
	}
}
