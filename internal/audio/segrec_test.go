package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 测试用的小参数：8kHz 单声道 + 短窗口，几十毫秒的挂起时间就能封段，
// 整套用例毫秒级跑完。分段逻辑本身和采样率无关。
func newTestRec(t *testing.T) *SegmentRecorder {
	t.Helper()
	r, err := NewSegmentRecorder(t.TempDir(), 8000, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.hangoverMS = 100
	r.maxSegMS = 2000
	r.prerollMS = 300
	r.minVoicedMS = 100
	return r
}

// feed 按 20ms 一帧喂进 ms 毫秒、峰值 amp 的信号，模拟解码协程的调用节奏。
func feed(t *testing.T, r *SegmentRecorder, ms, amp int) {
	t.Helper()
	const chunkMS = 20
	frame := make([]int16, r.samples(chunkMS))
	for i := range frame {
		if i%2 == 0 {
			frame[i] = int16(amp)
		} else {
			frame[i] = int16(-amp)
		}
	}
	for sent := 0; sent < ms; sent += chunkMS {
		r.Write(frame)
	}
}

func wavCount(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".wav" {
			n++
		}
	}
	return n
}

func TestSegmentRecorderClosesOnSilence(t *testing.T) {
	r := newTestRec(t)
	defer r.Close()

	feed(t, r, 400, 0) // 静音期间不该开段
	if wavCount(t, r.Dir()) != 0 {
		t.Fatalf("没人说话就建了文件")
	}
	feed(t, r, 400, 8000) // 说话
	if got := len(r.List()); got != 0 {
		t.Fatalf("话还没说完就封段了：%d 段", got)
	}

	feed(t, r, 200, 0) // 静音超过挂起时间，封段
	list := r.List()
	if len(list) != 1 {
		t.Fatalf("期望 1 段，实际 %d", len(list))
	}
	seg := list[0]

	// 预滚 300 + 语音 400 + 挂起 100
	if seg.DurMS < 700 || seg.DurMS > 900 {
		t.Errorf("段时长 %dms，期望 800ms 上下", seg.DurMS)
	}
	// 8000/32767 ≈ -12dBFS
	if seg.PeakDB < -14 || seg.PeakDB > -10 {
		t.Errorf("峰值 %.1fdBFS，期望 -12 上下", seg.PeakDB)
	}

	st, err := os.Stat(filepath.Join(r.Dir(), seg.Name))
	if err != nil {
		t.Fatalf("索引里的文件不存在：%v", err)
	}
	// 44 字节头 + 每样本 2 字节
	if want := int64(44 + seg.DurMS*8000/1000*2); st.Size() != want {
		t.Errorf("文件大小 %d，按 %dms 算应为 %d", st.Size(), seg.DurMS, want)
	}
}

// 判为有声时字头已经过去了几十毫秒，预滚就是拿来补这一截的。
func TestSegmentRecorderKeepsPreroll(t *testing.T) {
	run := func(prerollMS int) int {
		r := newTestRec(t)
		defer r.Close()
		r.prerollMS = prerollMS

		feed(t, r, 400, 0) // 足够填满预滚环
		feed(t, r, 400, 8000)
		feed(t, r, 200, 0)

		list := r.List()
		if len(list) != 1 {
			t.Fatalf("预滚 %dms：期望 1 段，实际 %d", prerollMS, len(list))
		}
		return list[0].DurMS
	}

	with, without := run(300), run(0)
	if without < 400 {
		t.Fatalf("不带预滚的段只有 %dms，语音本身就有 400ms", without)
	}
	if with-without < 280 {
		t.Errorf("预滚没生效：带预滚 %dms，不带 %dms，差值应接近 300ms", with, without)
	}
}

func TestSegmentRecorderDropsShortSegment(t *testing.T) {
	r := newTestRec(t)
	defer r.Close()
	r.prerollMS = 0 // 排除预滚，确认判据看的是有效语音而不是文件长度

	feed(t, r, 60, 8000) // 只有 60ms 有声，minVoicedMS 是 100
	feed(t, r, 200, 0)

	if got := r.List(); len(got) != 0 {
		t.Errorf("零碎声音被当成一段留下了：%+v", got)
	}
	if n := wavCount(t, r.Dir()); n != 0 {
		t.Errorf("短段的文件没删干净：目录里还有 %d 个", n)
	}
}

func TestSegmentRecorderKeepsOnlyRecent(t *testing.T) {
	r := newTestRec(t)
	defer r.Close()
	r.prerollMS = 0
	r.maxSegments = 50

	var created []string // 旧→新
	for i := 0; i < 55; i++ {
		feed(t, r, 200, 8000)
		feed(t, r, 100, 0)
		list := r.List()
		if len(list) == 0 {
			t.Fatalf("第 %d 段没有生成", i)
		}
		created = append(created, list[0].Name)
	}

	list := r.List()
	if len(list) != 50 {
		t.Fatalf("索引里有 %d 段，上限是 50", len(list))
	}
	if n := wavCount(t, r.Dir()); n != 50 {
		t.Fatalf("目录里有 %d 个文件，上限是 50", n)
	}
	// 留下的必须是最近的 50 段，且新→旧
	want := created[len(created)-50:]
	for i, info := range list {
		if w := want[len(want)-1-i]; info.Name != w {
			t.Fatalf("第 %d 条是 %s，期望 %s（应按新→旧排，且裁掉的是最旧的）", i, info.Name, w)
		}
	}
}

// 重启之后上一次运行留下的段还要能点开听，所以索引得从目录重建。
func TestSegmentRecorderRebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	r, err := NewSegmentRecorder(dir, 8000, 1)
	if err != nil {
		t.Fatal(err)
	}
	r.hangoverMS = 100
	r.prerollMS = 0
	r.minVoicedMS = 100
	feed(t, r, 400, 8000)
	r.Close()

	again, err := NewSegmentRecorder(dir, 8000, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	list := again.List()
	if len(list) != 1 {
		t.Fatalf("重建后有 %d 段，期望 1 段", len(list))
	}
	if list[0].DurMS < 380 || list[0].DurMS > 420 {
		t.Errorf("按文件大小折算的时长 %dms，期望 400ms 上下", list[0].DurMS)
	}
	if list[0].Time.IsZero() {
		t.Error("时间没从文件名里解析出来")
	}
}

func TestPruneByTotalBytes(t *testing.T) {
	// 段数限制挡不住长段：还要按目录总体积兜底。
	dir := t.TempDir()
	r, err := NewSegmentRecorder(dir, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	r.maxBytes = 4096 // 测试用小上限

	for i := 0; i < 5; i++ { // 每个文件 2KB，5 个共 10KB 远超上限
		name := fmt.Sprintf("2026010%d-000000.wav", i+1)
		os.WriteFile(filepath.Join(dir, name), make([]byte, 2048), 0o644)
	}
	r.prune()

	left := r.names()
	var total int64
	for _, n := range left {
		fi, _ := os.Stat(filepath.Join(dir, n))
		total += fi.Size()
	}
	if total > 4096 {
		t.Fatalf("prune 后总体积 %d 仍超上限 4096", total)
	}
	if len(left) == 0 {
		t.Fatal("不该全删光")
	}
	// 留下的必须是最新的
	if left[len(left)-1] != "20260105-000000.wav" {
		t.Fatalf("删错方向，最新段没保住：%v", left)
	}
}
