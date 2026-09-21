package hubclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hueshu/relaymic/internal/signaling"
)

// fakeHub 是最小可用的公网控制面替身：认证、发码、转发 offer/answer。
type fakeHub struct {
	t      *testing.T
	token  string
	server *httptest.Server

	mu         sync.Mutex
	connects   int
	codes      []string
	answers    []json.RawMessage
	sessions   []string
	dropAfter  int // 第几次连接发完码就把连接掐掉，0 表示不掐
	iceServers []signaling.ICEServer
}

func newFakeHub(t *testing.T, token string) *fakeHub {
	t.Helper()
	h := &fakeHub{t: t, token: token}
	h.server = httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(h.server.Close)
	return h
}

func (h *fakeHub) url() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + "/ws/receiver"
}

func (h *fakeHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.connects
}

func (h *fakeHub) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+h.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()

	h.mu.Lock()
	h.connects++
	n := h.connects
	h.mu.Unlock()

	code := "10000" + string(rune('0'+n))
	h.mu.Lock()
	h.codes = append(h.codes, code)
	h.mu.Unlock()

	if err := write(ctx, conn, map[string]any{"type": "waiting", "code": code, "expiresInSec": 300}); err != nil {
		return
	}
	if h.dropAfter > 0 && n == h.dropAfter {
		return
	}

	session := "sess-" + code
	h.mu.Lock()
	h.sessions = append(h.sessions, session)
	h.mu.Unlock()
	if err := write(ctx, conn, map[string]any{"type": "joined", "session": session, "iceServers": h.iceServers}); err != nil {
		return
	}
	if err := write(ctx, conn, map[string]any{"type": "offer", "session": session, "sdp": map[string]any{"type": "offer", "sdp": "v=0 fake"}}); err != nil {
		return
	}

	for {
		var msg struct {
			Type    string          `json:"type"`
			Session string          `json:"session"`
			SDP     json.RawMessage `json:"sdp"`
		}
		if err := read(ctx, conn, &msg); err != nil {
			return
		}
		if msg.Type != "answer" {
			continue
		}
		h.mu.Lock()
		h.answers = append(h.answers, msg.SDP)
		h.mu.Unlock()
	}
}

func TestRunPassesSessionICEServersToReceiver(t *testing.T) {
	token := "test-token"
	hub := newFakeHub(t, token)
	hub.iceServers = []signaling.ICEServer{
		{URLs: []string{"stun:stun.example.com:3478"}},
		{URLs: []string{"turn:turn.example.com:3478?transport=udp"}, Username: "1700000600:session", Credential: "short-lived"},
	}
	client := newTestClient(t, hub, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	joined := make(chan []signaling.ICEServer, 1)
	go func() {
		_ = client.Run(ctx, Handlers{
			OnJoined: func(_ string, servers []signaling.ICEServer) { joined <- servers },
		})
	}()

	servers := <-joined
	if len(servers) != 2 {
		t.Fatalf("session ICE servers = %+v, want STUN and TURN", servers)
	}
	if got, want := servers[1].Credential, "short-lived"; got != want {
		t.Fatalf("TURN credential = %q, want %q", got, want)
	}
}

func write(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func read(ctx context.Context, conn *websocket.Conn, v any) error {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (h *fakeHub) snapshot() (codes []string, sessions []string, answers []json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.codes...), append([]string(nil), h.sessions...), append([]json.RawMessage(nil), h.answers...)
}

func newTestClient(t *testing.T, hub *fakeHub, token string) *Client {
	t.Helper()
	client, err := New(Config{
		URL:          hub.url(),
		Token:        token,
		ReconnectMin: 10 * time.Millisecond,
		ReconnectMax: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func TestRunRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("New() accepted an empty config")
	}
	if _, err := New(Config{URL: "wss://mic.example.com/ws/receiver"}); err == nil {
		t.Fatal("New() accepted a config without a token")
	}
	if _, err := New(Config{Token: "t"}); err == nil {
		t.Fatal("New() accepted a config without a URL")
	}
}

func TestRunReportsCodeJoinsAndAnswersOffers(t *testing.T) {
	token := "test-token"
	hub := newFakeHub(t, token)
	client := newTestClient(t, hub, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	codes := make(chan string, 4)
	joined := make(chan string, 4)
	answered := make(chan string, 4)

	go func() {
		_ = client.Run(ctx, Handlers{
			OnCode:   func(code string, _ time.Duration) { codes <- code },
			OnJoined: func(session string, _ []signaling.ICEServer) { joined <- session },
			OnOffer: func(_ context.Context, session string, offer json.RawMessage) (json.RawMessage, error) {
				if !strings.Contains(string(offer), "v=0 fake") {
					t.Errorf("offer = %s, want it relayed verbatim", offer)
				}
				answered <- session
				return json.RawMessage(`{"type":"answer","sdp":"v=0 fake answer"}`), nil
			},
		})
	}()

	if got := waitString(t, codes); len(got) != 6 {
		t.Fatalf("code = %q, want 6 digits", got)
	}
	session := waitString(t, joined)
	if got := waitString(t, answered); got != session {
		t.Fatalf("answered session = %q, want %q", got, session)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, _, answers := hub.snapshot()
		if len(answers) > 0 {
			if !strings.Contains(string(answers[0]), "v=0 fake answer") {
				t.Fatalf("hub received answer %s", answers[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hub never received the answer")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunReconnectsAfterTheHubDropsTheConnection(t *testing.T) {
	token := "test-token"
	hub := newFakeHub(t, token)
	hub.dropAfter = 1
	client := newTestClient(t, hub, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	codes := make(chan string, 8)
	go func() {
		_ = client.Run(ctx, Handlers{OnCode: func(code string, _ time.Duration) { codes <- code }})
	}()

	first := waitString(t, codes)
	second := waitString(t, codes)
	if first == second {
		t.Fatalf("reconnect produced the same code %q", first)
	}
	if hub.count() < 2 {
		t.Fatalf("hub saw %d connections, want at least 2", hub.count())
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	token := "test-token"
	hub := newFakeHub(t, token)
	client := newTestClient(t, hub, token)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx, Handlers{}) }()

	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return after the context was canceled")
	}
}

func TestRunSurfacesAuthenticationFailure(t *testing.T) {
	hub := newFakeHub(t, "right-token")
	client := newTestClient(t, hub, "wrong-token")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := client.Run(ctx, Handlers{})
	if err == nil {
		t.Fatal("Run() succeeded with a wrong token")
	}
	if !strings.Contains(err.Error(), "401") && !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("Run() error = %v, want it to name the rejected credential", err)
	}
}

func waitString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a callback")
		return ""
	}
}
