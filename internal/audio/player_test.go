package audio

import (
	"encoding/binary"
	"testing"
)

func TestRingCapsLatency(t *testing.T) {
	// 目标 100 样本，容量 400
	r := newRing(400, 100, 1)
	r.write(make([]int16, 100)) // 先攒够，进入正常播放

	// 模拟真实的时钟漂移：声卡匀速取 20，发送端每次给 21（快 5%）。
	// 没有收缩机制的话，延迟会一路涨到缓冲上限。
	out := make([]byte, 40)
	maxSeen := 0
	for i := 0; i < 500; i++ {
		r.write(make([]int16, 21))
		r.readInto(out, 20)
		if s, _, _ := r.stats(); s > maxSeen {
			maxSeen = s
		}
	}

	size, _, _ := r.stats()
	if maxSeen > 260 {
		t.Errorf("延迟失控：峰值堆到 %d 样本（目标 100）", maxSeen)
	}
	if size < 50 {
		t.Errorf("压过头了：%d 样本，会造成断续", size)
	}
}

func TestRingTrimsGently(t *testing.T) {
	// 单次收缩绝不能大到听得出来：上限是目标的十分之一。
	r := newRing(4000, 1000, 1)
	r.write(make([]int16, 3500)) // 一次性灌到远超阈值

	before, _, _ := r.stats()
	r.write(make([]int16, 20))
	after, _, _ := r.stats()

	trimmed := before + 20 - after
	if trimmed > 100 {
		t.Errorf("单次丢弃 %d 样本，超过目标的 1/10，会听成跳字", trimmed)
	}
}

func TestRingPrefillBeforePlayback(t *testing.T) {
	r := newRing(400, 100, 1)
	out := make([]byte, 40) // 20 样本

	// 还没攒够 prefill，应当输出静音而不是不完整的数据
	r.write(make([]int16, 10))
	for i := range out {
		out[i] = 0xFF
	}
	r.readInto(out, 20)
	for _, b := range out {
		if b != 0 {
			t.Fatal("攒够之前不应输出数据")
		}
	}

	// 攒够之后正常出数
	r.write(make([]int16, 200))
	r.readInto(out, 20)
	if size, _, _ := r.stats(); size == 0 {
		t.Error("攒够后应当正常消费")
	}
}

func TestRingSilenceIsNotStarvation(t *testing.T) {
	// 发送端开了 DTX，静音期不发包。声卡照样每隔几毫秒来要一次数据，
	// 缓冲当然是空的 —— 但那是没人说话，不是链路出问题。
	// 全算进欠载的话，说话停两秒就能记上几百次，这个数字就废了。
	r := newRing(400, 100, 1)
	out := make([]byte, 40) // 20 样本

	r.write(make([]int16, 100)) // 攒够起播
	for i := 0; i < 5; i++ {
		r.readInto(out, 20) // 正好消费完
	}

	for i := 0; i < 200; i++ {
		r.readInto(out, 20) // 源头静默
	}

	if _, _, starved := r.stats(); starved > 1 {
		t.Errorf("静音期记了 %d 次欠载，这个诊断数字会被冲垮", starved)
	}
}

func TestRingStillCountsRealStarvation(t *testing.T) {
	// 反过来：数据一直在来却总是接不上，才是真欠载。
	// 漏报比误报更糟 —— 会让人以为链路是好的。
	r := newRing(400, 100, 1)
	out := make([]byte, 40)

	for i := 0; i < 10; i++ {
		r.write(make([]int16, 100))
		for j := 0; j < 6; j++ {
			r.readInto(out, 20) // 第 6 次必然见底
		}
	}

	if _, _, starved := r.stats(); starved < 5 {
		t.Errorf("真欠载只记了 %d 次，漏报会掩盖链路问题", starved)
	}
}

func TestRingFadesOutOnSilence(t *testing.T) {
	// 波形从任意值硬切到零是一次宽频脉冲，听感就是短促的"biu"。
	// 见底后的输出必须从最后的样本值衰减下来，而不是直接归零。
	r := newRing(400, 100, 1)
	loud := make([]int16, 100)
	for i := range loud {
		loud[i] = 8000
	}
	r.write(loud)
	out := make([]byte, 200)
	r.readInto(out, 100) // 全部耗尽（末段已过淡入，达到全幅）

	r.readInto(out, 100) // 见底：应出衰减尾音
	first := int16(binary.LittleEndian.Uint16(out))
	last := int16(binary.LittleEndian.Uint16(out[198:]))
	if first < 4000 {
		t.Errorf("见底后第一个样本 %d，几乎是硬切到零，会响一声", first)
	}
	if last >= first {
		t.Errorf("尾音没有在衰减：首 %d 末 %d", first, last)
	}

	for i := 0; i < 10; i++ {
		r.readInto(out, 100)
	}
	if v := int16(binary.LittleEndian.Uint16(out[198:])); v > 8 {
		t.Errorf("衰减一秒后仍有 %d，应基本归零", v)
	}
}

func TestRingFadesInAfterSilence(t *testing.T) {
	// 起播（含 DTX 静音后的恢复）要淡入：从零直接跳进波形中段同样是脉冲。
	r := newRing(400, 100, 1)
	loud := make([]int16, 200)
	for i := range loud {
		loud[i] = 8000
	}
	r.write(loud)
	out := make([]byte, 40)
	r.readInto(out, 20)
	if first := int16(binary.LittleEndian.Uint16(out)); first > 2000 {
		t.Errorf("起播第一个样本 %d，没有淡入，会有脉冲", first)
	}
	for i := 0; i < 5; i++ {
		r.readInto(out, 20) // 累计 120 帧，淡入(96帧)已结束
	}
	if last := int16(binary.LittleEndian.Uint16(out[38:])); last != 8000 {
		t.Errorf("淡入结束后应回到全幅 8000，得到 %d", last)
	}
}

func TestRingStretchesWhenLow(t *testing.T) {
	// 缓冲跌破目标一半时要轻微拖时间（重复帧），让缓冲回升，
	// 而不是滑到见底断一次。0.5% 的变速换掉 150ms 的断口。
	r := newRing(4000, 1000, 1)
	r.write(make([]int16, 1000)) // 起播
	out := make([]byte, 800)     // 每次 400 帧

	// 消费到低于 prefill/2
	r.readInto(out, 400)
	r.readInto(out, 400) // 剩 200 < 500
	r.write(make([]int16, 250))

	before, _, _ := r.stats()
	r.readInto(out, 400) // 400 帧，应触发两次重复，消费 398
	after, _, _ := r.stats()
	consumed := before - after
	if consumed >= 400 {
		t.Fatalf("低水位没有拉伸：消费了 %d/400", consumed)
	}
	if 400-consumed > 4 {
		t.Fatalf("拉伸过猛：只消费 %d/400，会听出变速", consumed)
	}
}

func TestRingNoStretchWhenHealthy(t *testing.T) {
	// 水位健康时绝不能拉伸——那是白白增加延迟。
	r := newRing(4000, 1000, 1)
	r.write(make([]int16, 2000))
	out := make([]byte, 800)
	before, _, _ := r.stats()
	r.readInto(out, 400)
	after, _, _ := r.stats()
	if before-after != 400 {
		t.Fatalf("健康水位却拉伸了：消费 %d/400", before-after)
	}
}
