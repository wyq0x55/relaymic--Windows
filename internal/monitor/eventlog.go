package monitor

import (
	"bytes"
	"strings"
	"sync"
)

// maxPartialLine 是一行日志最多攒多久。日志是按行写的，但 io.Writer 不保证
// 一次调用就是一行；攒到这么长还没换行，就先当成一行收下，免得半行永远不出现在
// 页面上。
const maxPartialLine = 4096

// EventLog 留住最近若干条日志，供本机诊断页回看。
//
// 它是个 io.Writer，直接接在 log 的输出上：这样"页面上看到的"和"日志里写的"
// 是同一份事实，不会因为哪处忘了记事件而漏掉。它只活在内存里 —— 诊断页要的是
// "刚才发生了什么"，不是一份要留档的日志。
type EventLog struct {
	mu       sync.Mutex
	capacity int
	lines    []string
	partial  []byte
}

// NewEventLog 造一个能留住最近 capacity 行的日志缓冲。
func NewEventLog(capacity int) *EventLog {
	if capacity <= 0 {
		capacity = 1
	}
	return &EventLog{capacity: capacity, lines: make([]string, 0, capacity)}
}

// Write 收下一段日志。它永远不报错：诊断页挂了不该把日志也拖死。
func (l *EventLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.partial = append(l.partial, p...)
	for {
		i := bytes.IndexByte(l.partial, '\n')
		if i < 0 {
			break
		}
		l.push(string(l.partial[:i]))
		l.partial = l.partial[i+1:]
	}
	if len(l.partial) > maxPartialLine {
		l.push(string(l.partial))
		l.partial = l.partial[:0]
	}
	return len(p), nil
}

// push 追加一行，超容量就丢最旧的。
func (l *EventLog) push(line string) {
	// Windows 上日志行尾会带 \r，留着它页面里会多一个空行。
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return
	}
	if len(l.lines) >= l.capacity {
		copy(l.lines, l.lines[1:])
		l.lines = l.lines[:len(l.lines)-1]
	}
	l.lines = append(l.lines, line)
}

// Recent 返回最近的日志，最旧的在前面。
func (l *EventLog) Recent() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.lines))
	copy(out, l.lines)
	return out
}