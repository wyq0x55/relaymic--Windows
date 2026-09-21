package monitor

import (
	"testing"
	"time"
)

func TestPairingStateShowsActiveCodeOnlyToLocalViewer(t *testing.T) {
	state := NewPairingState()
	now := time.Date(2026, time.September, 21, 10, 0, 0, 0, time.UTC)
	state.Set("123456", now.Add(5*time.Minute))

	local := state.Snapshot(now, true)
	if !local.Active || local.Code != "123456" || local.ExpiresIn != 5*time.Minute {
		t.Fatalf("local snapshot = %+v, want active pairing code", local)
	}

	remote := state.Snapshot(now, false)
	if !remote.Active || remote.Code != "" || !remote.Hidden {
		t.Fatalf("remote snapshot = %+v, want active but redacted pairing code", remote)
	}
}

func TestPairingStateClearsUsedAndExpiredCode(t *testing.T) {
	state := NewPairingState()
	now := time.Date(2026, time.September, 21, 10, 0, 0, 0, time.UTC)
	state.Set("123456", now.Add(time.Second))

	expired := state.Snapshot(now.Add(time.Second), true)
	if expired.Active || expired.Code != "" || expired.ExpiresIn != 0 {
		t.Fatalf("expired snapshot = %+v, want no pairing code", expired)
	}

	state.Set("654321", now.Add(5*time.Minute))
	state.Clear()
	cleared := state.Snapshot(now, true)
	if cleared.Active || cleared.Code != "" || cleared.ExpiresIn != 0 {
		t.Fatalf("cleared snapshot = %+v, want no pairing code", cleared)
	}
}

func TestIsLoopbackRemoteAddr(t *testing.T) {
	for _, remoteAddr := range []string{"127.0.0.1:7420", "[::1]:7420"} {
		if !IsLoopbackRemoteAddr(remoteAddr) {
			t.Fatalf("IsLoopbackRemoteAddr(%q) = false, want true", remoteAddr)
		}
	}
	for _, remoteAddr := range []string{"192.0.2.10:7420", "invalid"} {
		if IsLoopbackRemoteAddr(remoteAddr) {
			t.Fatalf("IsLoopbackRemoteAddr(%q) = true, want false", remoteAddr)
		}
	}
}
