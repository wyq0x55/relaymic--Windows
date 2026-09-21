// Package monitor holds state that the receiver's local monitor may expose.
package monitor

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// PairingSnapshot is the monitor-safe view of the receiver's current pairing
// capability. Code is intentionally absent for non-loopback viewers.
type PairingSnapshot struct {
	Active    bool
	Code      string
	Hidden    bool
	ExpiresIn time.Duration
}

// PairingState records the one-time pairing code currently assigned by the
// Hub. It contains no receiver token or other long-lived credential.
type PairingState struct {
	mu        sync.RWMutex
	code      string
	expiresAt time.Time
}

// NewPairingState constructs an empty pairing-code state.
func NewPairingState() *PairingState {
	return &PairingState{}
}

// Set replaces the current pairing code and its absolute expiry time.
func (s *PairingState) Set(code string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = code
	s.expiresAt = expiresAt
}

// Clear removes a code that has been consumed or is no longer valid.
func (s *PairingState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = ""
	s.expiresAt = time.Time{}
}

// Snapshot returns the current code only when the caller is on loopback.
func (s *PairingState) Snapshot(now time.Time, allowCode bool) PairingSnapshot {
	s.mu.RLock()
	code, expiresAt := s.code, s.expiresAt
	s.mu.RUnlock()

	expiresIn := time.Until(expiresAt)
	if !now.IsZero() {
		expiresIn = expiresAt.Sub(now)
	}
	if code == "" || expiresAt.IsZero() || expiresIn <= 0 {
		return PairingSnapshot{}
	}
	if !allowCode {
		return PairingSnapshot{Active: true, Hidden: true, ExpiresIn: expiresIn}
	}
	return PairingSnapshot{Active: true, Code: code, ExpiresIn: expiresIn}
}

// IsLoopbackRemoteAddr reports whether a net/http RemoteAddr is local to this
// machine. It fails closed for malformed or non-IP addresses.
func IsLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}
