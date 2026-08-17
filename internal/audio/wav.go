package audio

import (
	"encoding/binary"
	"os"
	"sync"
)

// WAVWriter 把 int16 PCM 追加进一个 WAV 文件，Close 时回填长度。
//
// 这是诊断工具，不是产品功能：当"听感有杂音"和"统计全绿"打架时，
// 唯一的仲裁者是波形本身。录下处理链某一点的原始样本，
// 拼接跳变、驱动门的硬边缘、削顶，在波形里全都一目了然。
type WAVWriter struct {
	mu   sync.Mutex
	f    *os.File
	data int // 已写数据字节数
}

func NewWAVWriter(path string, sampleRate, channels int) (*WAVWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	// 44 字节标准头，长度字段先占位，Close 时回填。
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], uint16(channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(h[28:], uint32(sampleRate*channels*2))
	binary.LittleEndian.PutUint16(h[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	if _, err := f.Write(h); err != nil {
		f.Close()
		return nil, err
	}
	return &WAVWriter{f: f}, nil
}

func (w *WAVWriter) Write(pcm []int16) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return
	}
	buf := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	if _, err := w.f.Write(buf); err == nil {
		w.data += len(buf)
	}
}

func (w *WAVWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	// 回填 RIFF 总长和 data 段长
	b4 := make([]byte, 4)
	binary.LittleEndian.PutUint32(b4, uint32(36+w.data))
	w.f.WriteAt(b4, 4)
	binary.LittleEndian.PutUint32(b4, uint32(w.data))
	w.f.WriteAt(b4, 40)
	err := w.f.Close()
	w.f = nil
	return err
}
