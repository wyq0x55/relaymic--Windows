package signaling

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock 是能被推着走的时钟。加锁是因为它也会被服务端协程读 ——
// 测试里推时间的同时，Hub 那边可能正在取当前时间。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// sequentialCodes 让每个测试都能预知下一个配对码，同时保留"每次都不一样"的语义。
func sequentialCodes() func() (PairingCode, error) {
	n := 100000
	return func() (PairingCode, error) {
		n++
		return PairingCode(fmt.Sprintf("%06d", n)), nil
	}
}

func newTestRegistry(t *testing.T, cfg Config) (*Registry, *fakeClock, string) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	cfg.Now = clock.now
	if cfg.NewCode == nil {
		cfg.NewCode = sequentialCodes()
	}
	cfg.Receivers = append(cfg.Receivers, ReceiverConfig{Name: "公司电脑", Token: token})
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return reg, clock, token
}

func TestNewRegistryRejectsMalformedToken(t *testing.T) {
	if _, err := NewRegistry(Config{Receivers: []ReceiverConfig{{Name: "公司电脑", Token: "not-a-token"}}}); err == nil {
		t.Fatal("NewRegistry() accepted a token that is not 32 bytes of raw url-safe base64")
	}
}

func TestNewRegistryRejectsDuplicateReceiverNames(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	cfg := Config{Receivers: []ReceiverConfig{{Name: "公司电脑", Token: token}, {Name: "公司电脑", Token: token}}}
	if _, err := NewRegistry(cfg); err == nil {
		t.Fatal("NewRegistry() accepted two receivers with the same name")
	}
}

func TestAuthenticateAcceptsOnlyTheConfiguredToken(t *testing.T) {
	reg, _, token := newTestRegistry(t, Config{})

	receiver, ok := reg.Authenticate(token)
	if !ok {
		t.Fatal("Authenticate() rejected the configured token")
	}
	if receiver.Name() != "公司电脑" {
		t.Fatalf("Authenticate() name = %q, want 公司电脑", receiver.Name())
	}
	if receiver.ID() == "" {
		t.Fatal("Authenticate() returned an empty receiver ID")
	}

	for _, bad := range []string{"", " ", token + "x", token[:len(token)-1], "0000000000000000000000000000000000000000000"} {
		if _, ok := reg.Authenticate(bad); ok {
			t.Fatalf("Authenticate(%q) accepted a wrong token", bad)
		}
	}
}

func TestIssueCodeReturnsSixDigitsAndExpiry(t *testing.T) {
	reg, clock, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)

	code, expiresAt, err := reg.IssueCode(receiver.ID())
	if err != nil {
		t.Fatalf("IssueCode() error = %v", err)
	}
	if len(code) != PairingCodeDigits {
		t.Fatalf("IssueCode() = %q, want %d digits", code, PairingCodeDigits)
	}
	if want := clock.now().Add(PairingCodeTTL); !expiresAt.Equal(want) {
		t.Fatalf("IssueCode() expiry = %v, want %v", expiresAt, want)
	}
}

func TestIssueCodeReplacesThePreviousCode(t *testing.T) {
	reg, _, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)

	first, _, err := reg.IssueCode(receiver.ID())
	if err != nil {
		t.Fatalf("IssueCode() error = %v", err)
	}
	second, _, err := reg.IssueCode(receiver.ID())
	if err != nil {
		t.Fatalf("IssueCode() error = %v", err)
	}
	if first == second {
		t.Fatalf("IssueCode() reused %q", first)
	}
	if _, err := reg.Redeem("1.2.3.4", first); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("Redeem(old code) error = %v, want ErrPairingFailed", err)
	}
	if _, err := reg.Redeem("1.2.3.4", second); err != nil {
		t.Fatalf("Redeem(new code) error = %v", err)
	}
}

func TestRedeemConsumesTheCodeOnce(t *testing.T) {
	reg, _, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)
	code, _, _ := reg.IssueCode(receiver.ID())

	got, err := reg.Redeem("1.2.3.4", code)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if got.ID() != receiver.ID() {
		t.Fatalf("Redeem() receiver = %q, want %q", got.ID(), receiver.ID())
	}
	if _, err := reg.Redeem("1.2.3.4", code); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("second Redeem() error = %v, want ErrPairingFailed", err)
	}
}

func TestRedeemRejectsExpiredCode(t *testing.T) {
	reg, clock, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)
	code, _, _ := reg.IssueCode(receiver.ID())

	clock.advance(PairingCodeTTL)
	if _, err := reg.Redeem("1.2.3.4", code); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("Redeem() at TTL boundary error = %v, want ErrPairingFailed", err)
	}
}

func TestRedeemRejectsUnknownCodeWithTheSameError(t *testing.T) {
	reg, _, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)
	code, _, _ := reg.IssueCode(receiver.ID())

	unknown := PairingCode("000000")
	if code == unknown {
		unknown = "000001"
	}
	_, unknownErr := reg.Redeem("1.2.3.4", unknown)
	if !errors.Is(unknownErr, ErrPairingFailed) {
		t.Fatalf("Redeem(unknown) error = %v, want ErrPairingFailed", unknownErr)
	}
	// 未知码、过期码、已用码必须给出同一个错误，否则响应差异本身就是枚举预言机。
	_, _ = reg.Redeem("1.2.3.4", code)
	_, usedErr := reg.Redeem("1.2.3.4", code)
	if usedErr.Error() != unknownErr.Error() {
		t.Fatalf("used-code error %q differs from unknown-code error %q", usedErr, unknownErr)
	}
}

func TestRedeemRateLimitsPerClient(t *testing.T) {
	reg, clock, token := newTestRegistry(t, Config{MaxAttemptsPerMinute: 5})
	receiver, _ := reg.Authenticate(token)
	code, _, _ := reg.IssueCode(receiver.ID())

	for i := 0; i < 5; i++ {
		if _, err := reg.Redeem("9.9.9.9", "000000"); !errors.Is(err, ErrPairingFailed) {
			t.Fatalf("attempt %d error = %v, want ErrPairingFailed", i, err)
		}
	}
	// 第 6 次即使码是对的也必须被拦下：这正是"猜码"要付的代价。
	if _, err := reg.Redeem("9.9.9.9", code); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("Redeem() past the limit error = %v, want ErrPairingFailed", err)
	}
	// 限流按客户端计数，别人不该被连带封禁。
	if _, err := reg.Redeem("8.8.8.8", code); err != nil {
		t.Fatalf("Redeem() from another client error = %v", err)
	}
	// 一分钟窗口滑过之后恢复。
	clock.advance(time.Minute)
	next, _, _ := reg.IssueCode(receiver.ID())
	if _, err := reg.Redeem("9.9.9.9", next); err != nil {
		t.Fatalf("Redeem() after the window slid error = %v", err)
	}
}

func TestReleaseInvalidatesTheCode(t *testing.T) {
	reg, _, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)
	code, _, _ := reg.IssueCode(receiver.ID())

	reg.Release(receiver.ID())
	if _, err := reg.Redeem("1.2.3.4", code); !errors.Is(err, ErrPairingFailed) {
		t.Fatalf("Redeem() after Release error = %v, want ErrPairingFailed", err)
	}
	// Release 只作废当前码；同一 token 重连后必须还能拿到新码，
	// 否则接收端重启一次就要重新发凭据。
	if _, ok := reg.Authenticate(token); !ok {
		t.Fatal("Authenticate() failed after Release; a reconnecting receiver must still be accepted")
	}
	if _, _, err := reg.IssueCode(receiver.ID()); err != nil {
		t.Fatalf("IssueCode() after Release error = %v", err)
	}
}

func TestIssueCodeForUnknownReceiver(t *testing.T) {
	reg, _, _ := newTestRegistry(t, Config{})
	if _, _, err := reg.IssueCode("nope"); !errors.Is(err, ErrUnknownReceiver) {
		t.Fatalf("IssueCode() error = %v, want ErrUnknownReceiver", err)
	}
}

func TestRegistryIsSafeForConcurrentUse(t *testing.T) {
	reg, _, token := newTestRegistry(t, Config{})
	receiver, _ := reg.Authenticate(token)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_, _, _ = reg.IssueCode(receiver.ID())
		}
	}()
	for i := 0; i < 200; i++ {
		_, _ = reg.Redeem("1.2.3.4", "000000")
	}
	<-done
}
