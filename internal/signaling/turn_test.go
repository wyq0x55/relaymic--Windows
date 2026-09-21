package signaling

import (
	"testing"
	"time"
)

func TestTurnIssuerCreatesSessionBoundRESTCredentials(t *testing.T) {
	issuer, err := NewTurnIssuer(
		[]string{"turn:turn.example.com:3478?transport=udp"},
		[]byte("shared-secret"),
		10*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTurnIssuer() error = %v", err)
	}
	issuer.now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	servers := issuer.ICEServers("session-abc")
	if len(servers) != 1 {
		t.Fatalf("len(ICEServers()) = %d, want 1", len(servers))
	}
	if got, want := servers[0].Username, "1700000600:session-abc"; got != want {
		t.Fatalf("username = %q, want %q", got, want)
	}
	if got, want := servers[0].Credential, "qB06lG84dNePfUyAe60Uwa8ead4="; got != want {
		t.Fatalf("credential = %q, want RFC 5766 REST HMAC-SHA1 value", got)
	}
	if got, want := servers[0].URLs[0], "turn:turn.example.com:3478?transport=udp"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}

	other := issuer.ICEServers("other-session")
	if other[0].Username == servers[0].Username || other[0].Credential == servers[0].Credential {
		t.Fatal("different sessions received reusable TURN credentials")
	}
}

func TestNewTurnIssuerRejectsUnsafeConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		urls   []string
		secret []byte
		ttl    time.Duration
	}{
		{name: "no URLs", secret: []byte("secret"), ttl: time.Minute},
		{name: "non TURN URL", urls: []string{"stun:stun.example.com:3478"}, secret: []byte("secret"), ttl: time.Minute},
		{name: "empty secret", urls: []string{"turn:turn.example.com:3478"}, ttl: time.Minute},
		{name: "zero lifetime", urls: []string{"turn:turn.example.com:3478"}, secret: []byte("secret")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewTurnIssuer(tt.urls, tt.secret, tt.ttl); err == nil {
				t.Fatal("NewTurnIssuer() succeeded with unsafe configuration")
			}
		})
	}
}
