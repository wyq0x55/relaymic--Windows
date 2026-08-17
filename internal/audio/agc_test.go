package audio

import (
	"math"
	"testing"
)

// 喂 n 帧给定峰值的信号，返回最后一帧处理后的峰值。
func drive(agc *AGC, amplitude float64, frames int) float64 {
	const n = 960
	var last float64
	for f := 0; f < frames; f++ {
		pcm := make([]int16, n)
		for i := range pcm {
			pcm[i] = int16(math.Sin(float64(i)*0.05) * amplitude * math.MaxInt16)
		}
		agc.Process(pcm)
		last = 0
		for _, s := range pcm {
			v := math.Abs(float64(s)) / math.MaxInt16
			if v > last {
				last = v
			}
		}
	}
	return last
}

func TestAGCLiftsQuietSignal(t *testing.T) {
	agc := NewAGC()
	// 很小的输入（-40dBFS 左右），放够时间应被抬到接近目标
	// 上限 16x 是刻意的：再高就会把底噪一并抬成沙沙声。
	// -40dBFS 的弱信号抬到 -16dBFS，识别引擎已经够用。
	got := drive(agc, 0.01, 4000)
	if got < 0.12 {
		t.Errorf("安静信号没有被抬起来：峰值 %.3f，期望 >0.12（增益 %.1f）", got, agc.Gain())
	}
}

func TestAGCTamesLoudSignalFast(t *testing.T) {
	agc := NewAGC()
	// 先在安静信号上把增益抬起来，再突然给一个大信号
	drive(agc, 0.01, 4000)
	got := drive(agc, 0.9, 20)
	if got > 0.98 {
		t.Errorf("突发大信号没有被及时压住：峰值 %.3f", got)
	}
}

func TestAGCNeverClips(t *testing.T) {
	agc := NewAGC()
	for _, amp := range []float64{0.005, 0.05, 0.3, 0.9, 1.0} {
		got := drive(agc, amp, 50)
		if got > 1.0 {
			t.Errorf("幅度 %.3f 时发生回绕，峰值 %.3f", amp, got)
		}
	}
}

func TestAGCIgnoresSilence(t *testing.T) {
	agc := NewAGC()
	drive(agc, 0.2, 100)
	before := agc.Gain()
	drive(agc, 0.0001, 500) // 近乎静音
	if math.Abs(agc.Gain()-before) > 0.01 {
		t.Errorf("静音段增益发生了漂移：%.3f → %.3f", before, agc.Gain())
	}
}

func TestAGCGatesSteadyNoise(t *testing.T) {
	agc := NewAGC()
	// 先让增益爬起来，再喂持续底噪
	drive(agc, 0.02, 3000)
	noise := drive(agc, 0.002, 200) // -54dBFS 的持续底噪
	if noise > 0.05 {
		t.Errorf("底噪没有被压住：输出峰值 %.4f（增益 %.1fx）", noise, agc.Gain())
	}
}

func TestAGCKeepsWordTailAfterPause(t *testing.T) {
	agc := NewAGC()
	drive(agc, 0.05, 500)
	// 说话刚停的头几帧不应被立刻掐掉
	got := drive(agc, 0.003, 3)
	if got < 0.001 {
		t.Errorf("语音刚停就被噪声门切断：%.5f", got)
	}
}

// 浏览器实测发来的语音峰值约 -56dBFS。这类"正常但很轻"的信号
// 必须被抬起来，而不是被噪声门当作底噪掐掉。
func TestAGCDoesNotGateQuietSpeech(t *testing.T) {
	agc := NewAGC()
	got := drive(agc, 0.0016, 3000) // ≈ -56dBFS
	if got < 0.02 {
		t.Errorf("音量小的正常语音被掐掉了：输出 %.5f（增益 %.1fx）", got, agc.Gain())
	}
}

func TestAGCGateNeverJumps(t *testing.T) {
	// 门从关到开（或反向）必须渐变：一帧内跳 22dB 就是每句话开头的"啪"。
	agc := NewAGC()
	loud := make([]int16, 960)
	for i := range loud {
		loud[i] = 8000
	}
	quiet := make([]int16, 960)

	drive(agc, 8000, 10) // 出声，门开
	prev := agc.gate
	for i := 0; i < 60; i++ { // 静音 1.2s，门逐渐关
		agc.Process(quiet)
		if d := prev - agc.gate; d > 0.15 {
			t.Fatalf("关门第 %d 帧跳变 %.2f，会听到台阶", i, d)
		}
		prev = agc.gate
	}
	if agc.gate > 0.3 { // 低位目标 0.2，留收敛余量
		t.Fatalf("静音 1.2s 后门仍开着 %.2f", agc.gate)
	}

	for i := 0; i < 10; i++ { // 重新出声，门快开但仍渐变
		agc.Process(loud)
		// 开门 rate 0.7 单帧最大 0.56；真正防"啪"的是 prevEff 帧内
		// 逐样本渐变，这里只挡"一帧全开"的极端倒退。
		if d := agc.gate - prev; d > 0.75 {
			t.Fatalf("开门第 %d 帧跳变 %.2f", i, d)
		}
		prev = agc.gate
	}
	if agc.gate < 0.9 {
		t.Fatalf("出声 200ms 后门只开到 %.2f，会吞字头", agc.gate)
	}
}

func TestAGCGainRampsWithinFrame(t *testing.T) {
	// 增益变化要摊在帧内逐样本渐变。若整帧一个系数，帧边界的
	// 幅度台阶就是随语音节奏出现的"啪啪"声（B 点录音实测抓到过）。
	agc := NewAGC()
	loud := make([]int16, 960)
	for i := range loud {
		loud[i] = 1000
	}
	drive(agc, 200, 50) // 先在小信号上把增益养高

	// 突然来一帧大信号：增益会被 attack 快速收小。
	// 检查帧首样本和上一帧末样本的输出幅度连续（比值接近 1）。
	prev := make([]int16, 960)
	copy(prev, loud)
	agc.Process(prev)
	last := float64(prev[959]) / 1000 // 上一帧末端有效系数

	frame := make([]int16, 960)
	for i := range frame {
		frame[i] = 1000
	}
	agc.Process(frame)
	first := float64(frame[0]) / 1000 // 本帧首端有效系数

	if last == 0 {
		t.Fatal("测试前提失败：上一帧输出为零")
	}
	if r := first / last; r < 0.95 || r > 1.05 {
		t.Fatalf("帧边界系数跳变 %.2f -> %.2f（比值 %.2f），会听到台阶", last, first, r)
	}
}
