package signaling

import (
	"testing"
	"time"
)

func receiverIDOf(t *testing.T, hub *testHub) string {
	t.Helper()
	rec, ok := hub.reg.Authenticate(hub.token)
	if !ok {
		t.Fatal("Authenticate() 认不出这台接收端")
	}
	return rec.ID()
}

// 配对码过期后必须自己换一张。
//
// 码原本只在两处产生：接收端连上来、会话结束。可它只有 5 分钟，现场的人
// 慢一点，页面上就再也没有码可给 —— 只能去重启接收端。
func TestExpiredCodeIsReplacedWithoutReconnect(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	hub := newTestHubWithNow(t, clock.now)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	first := receiver.recv()
	id := receiverIDOf(t, hub)

	// 还没过期就不该换：换了等于把用户手里那张码当场作废。
	hub.server.refreshExpiredCodes(clock.now())
	if code, _, ok := hub.reg.LiveCode(id); !ok || string(code) != first.Code {
		t.Fatalf("没过期就换码了：code=%q ok=%v，want %q", code, ok, first.Code)
	}

	clock.advance(PairingCodeTTL + time.Second)
	hub.server.refreshExpiredCodes(clock.now())

	next := receiver.recv()
	if next.Type != TypeWaiting {
		t.Fatalf("换码消息 = %q，want %q", next.Type, TypeWaiting)
	}
	if next.Code == first.Code {
		t.Fatalf("换回来的还是旧码 %q", next.Code)
	}
	if want := int(PairingCodeTTL.Seconds()); next.ExpiresIn != want {
		t.Fatalf("expiresInSec = %d，want %d", next.ExpiresIn, want)
	}
}

// 通话中不换码：那会儿接收端不在等人配对，页面上冒出一张码只会让人以为
// 会话断了。
func TestRefreshLeavesAnActiveCallAlone(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	hub := newTestHubWithNow(t, clock.now)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	hub.pair(t, receiver, sender)
	id := receiverIDOf(t, hub)

	clock.advance(PairingCodeTTL + time.Second)
	hub.server.refreshExpiredCodes(clock.now())

	if code, _, ok := hub.reg.LiveCode(id); ok {
		t.Fatalf("通话中又发了一张码 %q", code)
	}
}
