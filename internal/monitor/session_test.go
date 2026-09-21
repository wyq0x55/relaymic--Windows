package monitor

import "testing"

func TestSessionStateTracksTheCurrentSession(t *testing.T) {
	state := NewSessionState()
	if got := state.Get(); got != "" {
		t.Fatalf("新建状态 = %q，want 空", got)
	}

	state.Set("sess-1")
	if got := state.Get(); got != "sess-1" {
		t.Fatalf("Get() = %q，want %q", got, "sess-1")
	}

	// 收尾的是别人的会话：不能把当前会话抹掉。
	state.Clear("sess-other")
	if got := state.Get(); got != "sess-1" {
		t.Fatalf("Clear(别人的会话) 之后 Get() = %q，want %q", got, "sess-1")
	}

	state.Clear("sess-1")
	if got := state.Get(); got != "" {
		t.Fatalf("Clear(自己) 之后 Get() = %q，want 空", got)
	}
}
