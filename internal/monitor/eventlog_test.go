package monitor

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestEventLogKeepsTheMostRecentLines(t *testing.T) {
	log := NewEventLog(3)
	for _, line := range []string{"一\n", "二\n", "三\n", "四\n"} {
		if _, err := log.Write([]byte(line)); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	got := log.Recent()
	want := []string{"二", "三", "四"}
	if len(got) != len(want) {
		t.Fatalf("Recent() = %v，want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Recent() = %v，want %v", got, want)
		}
	}
}

// io.Writer 不保证一次调用就是一行：日志被切开时不能把半行丢掉，也不能
// 把两行粘成一行。
func TestEventLogReassemblesSplitWrites(t *testing.T) {
	log := NewEventLog(10)
	_, _ = log.Write([]byte("17:00:00 已连"))
	_, _ = log.Write([]byte("接 wss://hub\n17:00:01 等待"))
	_, _ = log.Write([]byte("发送端\n"))

	got := log.Recent()
	if len(got) != 2 || got[0] != "17:00:00 已连接 wss://hub" || got[1] != "17:00:01 等待发送端" {
		t.Fatalf("Recent() = %v", got)
	}
}

// 一行一直不换行也得露面，否则卡住的半行在页面上永远是空的。
func TestEventLogSurfacesAStuckPartialLine(t *testing.T) {
	log := NewEventLog(10)
	_, _ = log.Write([]byte(strings.Repeat("x", maxPartialLine+1)))
	if len(log.Recent()) != 1 {
		t.Fatalf("Recent() = %v", log.Recent())
	}
}

func TestEventLogTrimsCarriageReturns(t *testing.T) {
	log := NewEventLog(10)
	_, _ = log.Write([]byte("一行\r\n\r\n"))
	got := log.Recent()
	if len(got) != 1 || got[0] != "一行" {
		t.Fatalf("Recent() = %v", got)
	}
}

// 日志是从多个协程写的，收日志的也得扛得住。
func TestEventLogIsSafeForConcurrentWriters(t *testing.T) {
	log := NewEventLog(50)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				fmt.Fprintf(log, "协程 %d 第 %d 行\n", id, j)
			}
		}(i)
	}
	wg.Wait()
	if got := len(log.Recent()); got != 50 {
		t.Fatalf("Recent() 有 %d 行，want 50", got)
	}
}

func TestRecentReturnsACopy(t *testing.T) {
	log := NewEventLog(10)
	_, _ = log.Write([]byte("原始\n"))
	got := log.Recent()
	got[0] = "被改过"
	if log.Recent()[0] != "原始" {
		t.Fatal("Recent() 把内部状态交出去了")
	}
}