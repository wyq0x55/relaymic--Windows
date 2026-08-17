package audio

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// segNameLayout 是段文件名里的时间格式。选它是因为字典序即时间序：
// 在目录里 ls 一下就是一条时间线，不必额外读文件属性。
const segNameLayout = "20060102-150405"

// SegmentInfo 描述一个已经封好的语音段。
type SegmentInfo struct {
	Name   string    // 文件名，不含目录
	Time   time.Time // 段开始时刻
	DurMS  int       // 段时长（含预滚）
	PeakDB float64   // 段内峰值电平（dBFS）
}

// SegmentRecorder 把持续送入的 PCM 按语音段切成一个个 WAV 文件。
//
// -record 录出来的是一整个大文件，而现场提的问题永远是"刚才那句为什么没识别出来"——
// 回答它需要按句翻，不是拖进度条。所以这里以静音为界切段：一句一个文件，
// 文件名就是时间，出了问题直接点最近那个听。
//
// 判据是帧峰值，喂进来的必须是增益之后的音频：浏览器实测发来的人声峰值只有
// -56dBFS 左右（见 AGC 的注释），拿 -36dBFS 的门去卡原始样本，一句话都录不到。
type SegmentRecorder struct {
	dir        string
	sampleRate int
	channels   int

	// 分段参数。跑真实音频用构造函数里的默认值，测试为了秒级跑完会调小。
	threshold   int   // 帧峰值超过它算有声
	hangoverMS  int   // 段尾连续静音这么久就封段
	maxSegMS    int   // 单段上限，免得持续噪声录出一个无限大的文件
	prerollMS   int   // 开段时回补多长的预滚
	minVoicedMS int   // 有效语音不足这么长的段直接扔掉
	maxSegments int   // 目录里最多留多少段
	maxBytes    int64 // 目录总体积上限。段数限制挡不住长段：单段可达
	// 5 分钟 ≈ 115MB，50 段最坏十几 GB。按字节再兜一道底

	mu    sync.Mutex
	index []SegmentInfo // 已封的段，旧→新

	// 预滚环形缓冲：始终保存最近 prerollMS 的样本。
	pre     []int16
	preHead int
	preSize int

	// 当前段的状态
	w       *WAVWriter
	name    string
	start   time.Time
	written int // 已写样本数（含预滚）
	voiced  int // 其中判为有声的样本数
	peak    int
	silent  int // 段尾连续静音的样本数

	// 同一秒内开出的上一个段，用于给文件名续序号。
	lastBase string
	lastSeq  int
}

// NewSegmentRecorder 在 dir 下开始按段录音，目录不存在就建。
// 目录里已有的 .wav 会被收进索引，上一次运行留下的段照样能回听。
func NewSegmentRecorder(dir string, sampleRate, channels int) (*SegmentRecorder, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建分段录音目录 %q: %w", dir, err)
	}
	r := &SegmentRecorder{
		dir:        dir,
		sampleRate: sampleRate,
		channels:   channels,
		threshold:  500,  // ≈ -36dBFS
		hangoverMS: 2000, // 说话中的停顿常有一秒多，短了会把一句话切成几截
		maxSegMS:   5 * 60 * 1000,
		// 300ms 是"开口之前"的余量。判为有声时字头已经过去了几十毫秒，
		// 不回补就会录成没有声母的半个字，回听时听不出问题出在哪。
		prerollMS:   300,
		minVoicedMS: 300, // 咳嗽、敲键盘都能越过阈值，但都不成句
		maxSegments: 50,
		maxBytes:    300 << 20, // 300MB/路，三路共约 1GB 封顶
	}
	r.index = r.scan()
	return r, nil
}

// Write 收一帧 PCM。由解码协程反复调用：绝大多数帧只是算个峰值再落盘，
// 建文件、扫目录、删旧段都只发生在段边界上。
func (r *SegmentRecorder) Write(pcm []int16) {
	if len(pcm) == 0 {
		return
	}

	peak := 0
	for _, s := range pcm {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	voiced := peak > r.threshold

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.w == nil {
		if !voiced {
			r.pushPreroll(pcm)
			return
		}
		if err := r.open(); err != nil {
			// 这是诊断功能，坏了不该拖垮通话：丢掉这一帧继续跑。
			r.pushPreroll(pcm)
			return
		}
	}

	r.w.Write(pcm)
	r.written += len(pcm)
	if peak > r.peak {
		r.peak = peak
	}
	if voiced {
		r.voiced += len(pcm)
		r.silent = 0
	} else {
		r.silent += len(pcm)
	}
	r.pushPreroll(pcm)

	if r.silent >= r.samples(r.hangoverMS) || r.written >= r.samples(r.maxSegMS) {
		r.finish()
	}
}

// List 返回已封好的段，新→旧：出问题时想听的永远是最近那句。
func (r *SegmentRecorder) List() []SegmentInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SegmentInfo, 0, len(r.index))
	for i := len(r.index) - 1; i >= 0; i-- {
		out = append(out, r.index[i])
	}
	return out
}

func (r *SegmentRecorder) Dir() string { return r.dir }

// Close 封掉正在录的那一段。太短的照样会被丢弃。
func (r *SegmentRecorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finish()
}

// open 开一个新段：先把超量的旧段裁掉，再把预滚灌进去。
func (r *SegmentRecorder) open() error {
	r.prune()
	now := time.Now()
	name, w, err := r.create(now)
	if err != nil {
		return err
	}
	r.w, r.name, r.start = w, name, now
	r.written, r.voiced, r.peak, r.silent = 0, 0, 0, 0
	if pre := r.drainPreroll(); len(pre) > 0 {
		w.Write(pre)
		r.written += len(pre)
	}
	return nil
}

// finish 封掉当前段：太短的直接删，剩下的进索引。
func (r *SegmentRecorder) finish() {
	if r.w == nil {
		return
	}
	r.w.Close()
	r.w = nil

	// 零碎声音留着，列表会被几十条一秒不到的记录刷屏，
	// 真正想回听的那句反而翻不着。
	if r.voiced < r.samples(r.minVoicedMS) {
		os.Remove(filepath.Join(r.dir, r.name))
		return
	}
	r.index = append(r.index, SegmentInfo{
		Name:   r.name,
		Time:   r.start,
		DurMS:  r.durMS(r.written),
		PeakDB: dbFS(r.peak),
	})
}

// create 打开段文件。同一秒里封两段时基名会撞车，加序号避让——
// 撞上的后果是 os.Create 把刚录好的那一段截断成空文件。
func (r *SegmentRecorder) create(now time.Time) (string, *WAVWriter, error) {
	base := now.Format(segNameLayout)
	seq := 0
	if base == r.lastBase {
		// 序号只能往上走。回收已被裁掉的小序号，会让新段在时间序里
		// 排到最前面，紧接着又被当成最旧的删掉。
		seq = r.lastSeq + 1
	}
	for ; ; seq++ {
		name := base + ".wav"
		if seq > 0 {
			name = fmt.Sprintf("%s-%d.wav", base, seq)
		}
		path := filepath.Join(r.dir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		w, err := NewWAVWriter(path, r.sampleRate, r.channels)
		if err != nil {
			return "", nil, err
		}
		r.lastBase, r.lastSeq = base, seq
		return name, w, nil
	}
}

// prune 删到"再开一段也不超过 maxSegments，且总体积不超 maxBytes"为止。
// 以目录为准而不是内存索引：里面可能还压着上一次运行留下的文件。
func (r *SegmentRecorder) prune() {
	names := r.names()
	var total int64
	sizes := make([]int64, len(names))
	for i, n := range names {
		if fi, err := os.Stat(filepath.Join(r.dir, n)); err == nil {
			sizes[i] = fi.Size()
			total += fi.Size()
		}
	}
	for i, n := range names {
		over := r.maxSegments >= 1 && len(names)-i >= r.maxSegments
		heavy := r.maxBytes > 0 && total > r.maxBytes
		if !over && !heavy {
			break
		}
		os.Remove(filepath.Join(r.dir, n))
		r.dropIndex(n)
		total -= sizes[i]
	}
}

func (r *SegmentRecorder) dropIndex(name string) {
	for i, info := range r.index {
		if info.Name == name {
			r.index = append(r.index[:i], r.index[i+1:]...)
			return
		}
	}
}

// names 列出目录里的段文件，旧→新。
func (r *SegmentRecorder) names() []string {
	ents, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".wav") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return segLess(out[i], out[j]) })
	return out
}

// scan 用目录里已有的文件重建索引。时长按文件大小折算，
// 峰值当时没记下来，只能留 0——这两个字段是给人扫一眼用的，不是判据。
func (r *SegmentRecorder) scan() []SegmentInfo {
	var out []SegmentInfo
	for _, name := range r.names() {
		t, _, ok := parseSegName(name)
		if !ok {
			continue
		}
		info := SegmentInfo{Name: name, Time: t}
		if st, err := os.Stat(filepath.Join(r.dir, name)); err == nil && st.Size() > 44 {
			info.DurMS = r.durMS(int(st.Size()-44) / 2)
		}
		out = append(out, info)
	}
	return out
}

// pushPreroll 把 pcm 存进预滚环，只留最近 prerollMS。
func (r *SegmentRecorder) pushPreroll(pcm []int16) {
	// 参数被调过（测试）就重开一个环，省得构造和使用两处各记一份长度。
	if n := r.samples(r.prerollMS); len(r.pre) != n {
		r.pre = make([]int16, n)
		r.preHead, r.preSize = 0, 0
	}
	if len(r.pre) == 0 {
		return
	}
	for _, s := range pcm {
		r.pre[(r.preHead+r.preSize)%len(r.pre)] = s
		if r.preSize < len(r.pre) {
			r.preSize++
		} else {
			r.preHead = (r.preHead + 1) % len(r.pre)
		}
	}
}

// drainPreroll 按时间顺序取出预滚并清空。
func (r *SegmentRecorder) drainPreroll() []int16 {
	out := make([]int16, r.preSize)
	for i := range out {
		out[i] = r.pre[(r.preHead+i)%len(r.pre)]
	}
	r.preHead, r.preSize = 0, 0
	return out
}

// samples 把毫秒换成交错样本数。
func (r *SegmentRecorder) samples(ms int) int {
	return r.sampleRate * r.channels * ms / 1000
}

func (r *SegmentRecorder) durMS(samples int) int {
	if r.sampleRate < 1 || r.channels < 1 {
		return 0
	}
	return samples * 1000 / (r.sampleRate * r.channels)
}

// parseSegName 从 "20060102-150405[-N].wav" 里取出时刻和同秒内的序号。
func parseSegName(name string) (time.Time, int, bool) {
	base := strings.TrimSuffix(name, ".wav")
	if len(base) < len(segNameLayout) {
		return time.Time{}, 0, false
	}
	t, err := time.ParseInLocation(segNameLayout, base[:len(segNameLayout)], time.Local)
	if err != nil {
		return time.Time{}, 0, false
	}
	seq := 0
	if rest := base[len(segNameLayout):]; rest != "" {
		if !strings.HasPrefix(rest, "-") {
			return time.Time{}, 0, false
		}
		if seq, err = strconv.Atoi(rest[1:]); err != nil {
			return time.Time{}, 0, false
		}
	}
	return t, seq, true
}

// segLess 按时间序比较两个段文件名。不能直接比字符串：'-' 小于 '.'，
// 同一秒里带序号的会排到不带序号的前面。
func segLess(a, b string) bool {
	ta, sa, oka := parseSegName(a)
	tb, sb, okb := parseSegName(b)
	if !oka || !okb {
		return a < b
	}
	if ta.Equal(tb) {
		return sa < sb
	}
	return ta.Before(tb)
}

// dbFS 把 int16 峰值换成 dBFS。全零的段给个地板值，免得 -Inf 打进日志。
func dbFS(peak int) float64 {
	if peak <= 0 {
		return -100
	}
	return 20 * math.Log10(float64(peak)/math.MaxInt16)
}
