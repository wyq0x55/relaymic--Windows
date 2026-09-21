package signaling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type testHub struct {
	server *Server
	http   *httptest.Server
	reg    *Registry
	token  string
	tokens []string
}

func newTestHub(t *testing.T) *testHub {
	t.Helper()
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	reg, err := NewRegistry(Config{Receivers: []ReceiverConfig{{Name: "公司电脑", Token: token}}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	srv, err := NewServer(ServerConfig{
		Registry:   reg,
		ICEServers: []ICEServer{{URLs: []string{"stun:stun.example.com:3478"}}},
		Page:       []byte("<!doctype html><title>RelayMic</title>"),
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return &testHub{server: srv, http: httpSrv, reg: reg, token: token, tokens: []string{token}}
}

func (h *testHub) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(h.http.URL, "http") + path
}

func (h *testHub) origin() string { return h.http.URL }

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (h *testHub) dialReceiver(t *testing.T, token string) (*wsClient, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	conn, resp, err := websocket.Dial(ctx, h.wsURL("/ws/receiver"), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, resp, err
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return &wsClient{t: t, conn: conn}, resp, nil
}

func (h *testHub) dialSender(t *testing.T, origin string) (*wsClient, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	conn, resp, err := websocket.Dial(ctx, h.wsURL("/ws/sender"), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, resp, err
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return &wsClient{t: t, conn: conn}, resp, nil
}

func (c *wsClient) send(msg Message) {
	c.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("marshal %+v: %v", msg, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("write %+v: %v", msg, err)
	}
}

// trySend 用在"服务端可能已经关掉连接"的用例里：这里关心的是服务端行为，
// 不是客户端写成功与否。
func (c *wsClient) trySend(msg Message) {
	c.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		c.t.Fatalf("marshal %+v: %v", msg, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *wsClient) recv() Message {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		c.t.Fatalf("decode %q: %v", data, err)
	}
	return msg
}

// expectClosed 断言对端把连接关掉了，而不是继续留在协议里。
func (c *wsClient) expectClosed() {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := c.conn.Read(ctx); err == nil {
		c.t.Fatal("read succeeded, want the hub to close the connection")
	}
}

func (h *testHub) pair(t *testing.T, receiver *wsClient, sender *wsClient) (code string, session string) {
	t.Helper()
	waiting := receiver.recv()
	if waiting.Type != TypeWaiting {
		t.Fatalf("receiver first message = %q, want %q", waiting.Type, TypeWaiting)
	}
	sender.send(Message{Type: TypePair, Code: waiting.Code})
	paired := sender.recv()
	if paired.Type != TypePaired {
		t.Fatalf("sender second message = %q (%s), want %q", paired.Type, paired.Error, TypePaired)
	}
	joined := receiver.recv()
	if joined.Type != TypeJoined || joined.Session != paired.Session {
		t.Fatalf("receiver join = %+v, want joined %s", joined, paired.Session)
	}
	return waiting.Code, paired.Session
}

func TestReceiverGetsCodeAfterAuthenticating(t *testing.T) {
	hub := newTestHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	msg := receiver.recv()
	if msg.Type != TypeWaiting {
		t.Fatalf("first message type = %q, want %q", msg.Type, TypeWaiting)
	}
	if len(msg.Code) != PairingCodeDigits {
		t.Fatalf("code = %q, want %d digits", msg.Code, PairingCodeDigits)
	}
	if want := int(PairingCodeTTL.Seconds()); msg.ExpiresIn != want {
		t.Fatalf("expiresInSec = %d, want %d", msg.ExpiresIn, want)
	}
}

func TestPairedEndpointsReceiveSameEphemeralTURNCredentials(t *testing.T) {
	hub := newTestHub(t)
	issuer, err := NewTurnIssuer(
		[]string{"turn:turn.example.com:3478?transport=udp"},
		[]byte("shared-secret"),
		10*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTurnIssuer() error = %v", err)
	}
	issuer.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	hub.server.turnIssuer = issuer

	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	waiting := receiver.recv()
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	sender.send(Message{Type: TypePair, Code: waiting.Code})
	paired := sender.recv()
	joined := receiver.recv()

	if paired.Type != TypePaired || joined.Type != TypeJoined {
		t.Fatalf("pair messages = %+v / %+v", paired, joined)
	}
	if len(paired.ICEServers) != 2 || len(joined.ICEServers) != 2 {
		t.Fatalf("TURN-enabled pair must include STUN and TURN: %+v / %+v", paired.ICEServers, joined.ICEServers)
	}
	if paired.ICEServers[1].Username != joined.ICEServers[1].Username ||
		paired.ICEServers[1].Credential != joined.ICEServers[1].Credential ||
		strings.Join(paired.ICEServers[1].URLs, ",") != strings.Join(joined.ICEServers[1].URLs, ",") {
		t.Fatalf("sender and receiver got different TURN credentials: %+v / %+v", paired.ICEServers[1], joined.ICEServers[1])
	}
	if got, want := paired.ICEServers[1].Username, "1700000600:"+paired.Session; got != want {
		t.Fatalf("TURN username = %q, want %q", got, want)
	}
}

func TestPublicICEEndpointDoesNotExposeSessionTURNCredentials(t *testing.T) {
	hub := newTestHub(t)
	issuer, err := NewTurnIssuer([]string{"turn:turn.example.com:3478"}, []byte("shared-secret"), time.Minute)
	if err != nil {
		t.Fatalf("NewTurnIssuer() error = %v", err)
	}
	hub.server.turnIssuer = issuer

	resp, err := http.Get(hub.http.URL + "/api/ice")
	if err != nil {
		t.Fatalf("GET /api/ice: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		ICEServers []ICEServer `json:"iceServers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode ICE response: %v", err)
	}
	if len(body.ICEServers) != 1 || body.ICEServers[0].Username != "" || body.ICEServers[0].Credential != "" {
		t.Fatalf("public ICE response leaked session TURN credentials: %+v", body.ICEServers)
	}
}

func TestReceiverWithoutTokenIsRejected(t *testing.T) {
	hub := newTestHub(t)
	if _, resp, err := hub.dialReceiver(t, ""); err == nil {
		t.Fatal("dialReceiver() without a token succeeded")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
	if _, resp, err := hub.dialReceiver(t, "wrong-token"); err == nil {
		t.Fatal("dialReceiver() with a wrong token succeeded")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}

func TestSenderOriginMustBeAllowed(t *testing.T) {
	hub := newTestHub(t)
	if _, resp, err := hub.dialSender(t, "https://evil.example"); err == nil {
		t.Fatal("dialSender() from a foreign origin succeeded")
	} else if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
	if _, resp, err := hub.dialSender(t, ""); err == nil {
		t.Fatal("dialSender() without an Origin succeeded")
	} else if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

func TestPairingRelaysOfferAndAnswer(t *testing.T) {
	hub := newTestHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	_, session := hub.pair(t, receiver, sender)

	offer := json.RawMessage(`{"type":"offer","sdp":"v=0 fake offer"}`)
	sender.send(Message{Type: TypeOffer, SDP: offer})
	got := receiver.recv()
	if got.Type != TypeOffer || got.Session != session {
		t.Fatalf("receiver got %+v, want offer for %s", got, session)
	}
	if string(got.SDP) != string(offer) {
		t.Fatalf("offer SDP = %s, want it relayed byte for byte: %s", got.SDP, offer)
	}

	answer := json.RawMessage(`{"type":"answer","sdp":"v=0 fake answer"}`)
	receiver.send(Message{Type: TypeAnswer, Session: session, SDP: answer})
	back := sender.recv()
	if back.Type != TypeAnswer || back.Session != session {
		t.Fatalf("sender got %+v, want answer for %s", back, session)
	}
	if string(back.SDP) != string(answer) {
		t.Fatalf("answer SDP = %s, want %s", back.SDP, answer)
	}
}

func TestWrongCodeUsesOneUniformErrorAndCloses(t *testing.T) {
	hub := newTestHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	waiting := receiver.recv()

	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	for i := 0; i < MaxPairingAttemptsPerConn; i++ {
		sender.send(Message{Type: TypePair, Code: "000000"})
		msg := sender.recv()
		if msg.Type != TypeError || msg.Error != PairingFailedText {
			t.Fatalf("attempt %d: got %+v, want a uniform %q error", i+1, msg, PairingFailedText)
		}
	}
	// 第 N+1 次不再给机会：连接直接关掉，避免在线穷举。
	sender.trySend(Message{Type: TypePair, Code: waiting.Code})
	sender.expectClosed()

	// 关掉的只是那条连接；接收端的码没有被消费，正常发送端仍能用它配对。
	good, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	good.send(Message{Type: TypePair, Code: waiting.Code})
	if paired := good.recv(); paired.Type != TypePaired {
		t.Fatalf("paired = %+v, want %q", paired, TypePaired)
	}
}

func TestSenderDisconnectEndsSessionAndIssuesFreshCode(t *testing.T) {
	hub := newTestHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	firstCode, session := hub.pair(t, receiver, sender)

	if err := sender.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close sender: %v", err)
	}

	left := receiver.recv()
	if left.Type != TypeLeft || left.Session != session {
		t.Fatalf("receiver got %+v, want left %s", left, session)
	}
	next := receiver.recv()
	if next.Type != TypeWaiting {
		t.Fatalf("receiver got %+v, want a fresh %q", next, TypeWaiting)
	}
	if next.Code == firstCode {
		t.Fatalf("fresh code = %q, want a code different from the consumed %q", next.Code, firstCode)
	}
}

func TestReceiverDisconnectClosesTheSession(t *testing.T) {
	hub := newTestHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	_, session := hub.pair(t, receiver, sender)

	if err := receiver.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close receiver: %v", err)
	}

	closed := sender.recv()
	if closed.Type != TypeClosed || closed.Session != session {
		t.Fatalf("sender got %+v, want closed %s", closed, session)
	}
	sender.expectClosed()
}

func TestReceiverReconnectReplacesTheOldConnection(t *testing.T) {
	hub := newTestHub(t)
	first, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	first.recv()

	second, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	// 新连接立刻拿到自己的码；旧连接被踢掉，避免两个房间同时挂着同一台机器。
	if msg := second.recv(); msg.Type != TypeWaiting {
		t.Fatalf("new connection got %+v, want %q", msg, TypeWaiting)
	}
	first.expectClosed()
}

func TestSenderMustPairBeforeSendingAnOffer(t *testing.T) {
	hub := newTestHub(t)
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	sender.trySend(Message{Type: TypeOffer, SDP: json.RawMessage(`{"type":"offer"}`)})
	sender.expectClosed()
}

func TestOversizedMessageIsRejected(t *testing.T) {
	hub := newTestHub(t)
	receiver, _, err := hub.dialReceiver(t, hub.token)
	if err != nil {
		t.Fatalf("dialReceiver() error = %v", err)
	}
	sender, _, err := hub.dialSender(t, hub.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	hub.pair(t, receiver, sender)

	huge := strings.Repeat("a", MaxMessageBytes)
	sender.trySend(Message{Type: TypeOffer, SDP: json.RawMessage(`"` + huge + `"`)})
	sender.expectClosed()
}

func TestICEServersAreServedOverHTTPS(t *testing.T) {
	hub := newTestHub(t)
	resp, err := http.Get(hub.http.URL + "/api/ice")
	if err != nil {
		t.Fatalf("GET /api/ice: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		ICEServers []ICEServer `json:"iceServers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.ICEServers) != 1 || body.ICEServers[0].URLs[0] != "stun:stun.example.com:3478" {
		t.Fatalf("iceServers = %+v", body.ICEServers)
	}
}

func TestPageIsServedAtRoot(t *testing.T) {
	hub := newTestHub(t)
	resp, err := http.Get(hub.http.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// newMultiTestHub 造一个挂多台接收端的 Hub，用来验证会话之间互不串台。
func newMultiTestHub(t *testing.T, names ...string) (*testHub, []string) {
	t.Helper()
	tokens := make([]string, 0, len(names))
	receivers := make([]ReceiverConfig, 0, len(names))
	for _, name := range names {
		token, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken() error = %v", err)
		}
		tokens = append(tokens, token)
		receivers = append(receivers, ReceiverConfig{Name: name, Token: token})
	}
	reg, err := NewRegistry(Config{Receivers: receivers})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	srv, err := NewServer(ServerConfig{
		Registry:   reg,
		ICEServers: []ICEServer{{URLs: []string{"stun:stun.example.com:3478"}}},
		Page:       []byte("<!doctype html><title>RelayMic</title>"),
	})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return &testHub{server: srv, http: httpSrv, reg: reg, tokens: tokens}, tokens
}

// pairWith 让一台接收端和自己的发送端配成一对，返回发送端和两侧的配对消息。
func (h *testHub) pairWith(t *testing.T, receiver *wsClient) (*wsClient, Message, Message) {
	t.Helper()
	waiting := receiver.recv()
	if waiting.Type != TypeWaiting {
		t.Fatalf("receiver first message = %q, want %q", waiting.Type, TypeWaiting)
	}
	sender, _, err := h.dialSender(t, h.origin())
	if err != nil {
		t.Fatalf("dialSender() error = %v", err)
	}
	sender.send(Message{Type: TypePair, Code: waiting.Code})
	paired := sender.recv()
	if paired.Type != TypePaired {
		t.Fatalf("sender paired = %+v, want %q", paired, TypePaired)
	}
	joined := receiver.recv()
	if joined.Type != TypeJoined {
		t.Fatalf("receiver joined = %+v, want %q", joined, TypeJoined)
	}
	return sender, paired, joined
}

// expectSDP 断言收到的就是这条 SDP。
//
// 串台的检测靠内容而不是"读超时"：coder/websocket 读超时会直接关掉连接，
// 用超时去证明"没收到"会把后面要用的连接一起毁掉。
func expectSDP(t *testing.T, got Message, wantType string, want json.RawMessage) {
	t.Helper()
	if got.Type != wantType {
		t.Fatalf("消息类型 = %q（%s），want %q", got.Type, got.Error, wantType)
	}
	if string(got.SDP) != string(want) {
		t.Fatalf("SDP = %s，want %s（疑似串台）", got.SDP, want)
	}
}

func TestConcurrentSessionsStayIsolated(t *testing.T) {
	hub, tokens := newMultiTestHub(t, "A-PC", "B-PC")

	recvA, _, err := hub.dialReceiver(t, tokens[0])
	if err != nil {
		t.Fatalf("dialReceiver(A) error = %v", err)
	}
	senderA, pairedA, _ := hub.pairWith(t, recvA)

	recvB, _, err := hub.dialReceiver(t, tokens[1])
	if err != nil {
		t.Fatalf("dialReceiver(B) error = %v", err)
	}
	senderB, pairedB, _ := hub.pairWith(t, recvB)

	if pairedA.Session == pairedB.Session {
		t.Fatalf("两条会话共用了 session id %q", pairedA.Session)
	}

	// A 的 offer 只能到 A 的接收端。
	offerA := json.RawMessage(`{"type":"offer","sdp":"v=0 A"}`)
	senderA.send(Message{Type: TypeOffer, SDP: offerA})
	expectSDP(t, recvA.recv(), TypeOffer, offerA)

	// A 的 answer 只能到 A 的发送端。
	answerA := json.RawMessage(`{"type":"answer","sdp":"v=0 A answer"}`)
	recvA.send(Message{Type: TypeAnswer, Session: pairedA.Session, SDP: answerA})
	expectSDP(t, senderA.recv(), TypeAnswer, answerA)

	// B 走一轮完整往返：如果 A 的 offer/answer 被串过来，这里读到的就不是自己的 SDP。
	offerB := json.RawMessage(`{"type":"offer","sdp":"v=0 B"}`)
	senderB.send(Message{Type: TypeOffer, SDP: offerB})
	expectSDP(t, recvB.recv(), TypeOffer, offerB)

	answerB := json.RawMessage(`{"type":"answer","sdp":"v=0 B answer"}`)
	recvB.send(Message{Type: TypeAnswer, Session: pairedB.Session, SDP: answerB})
	expectSDP(t, senderB.recv(), TypeAnswer, answerB)

	// A 的发送端断开：B 的会话必须原样继续。
	if err := senderA.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close sender A: %v", err)
	}
	offerB2 := json.RawMessage(`{"type":"offer","sdp":"v=0 B after A left"}`)
	senderB.send(Message{Type: TypeOffer, SDP: offerB2})
	expectSDP(t, recvB.recv(), TypeOffer, offerB2)

	// A 的接收端重连拿新码，也不能影响 B 的会话。
	if err := recvA.conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatalf("close receiver A: %v", err)
	}
	recvA2, _, err := hub.dialReceiver(t, tokens[0])
	if err != nil {
		t.Fatalf("re-dial receiver A: %v", err)
	}
	if waiting := recvA2.recv(); waiting.Type != TypeWaiting || len(waiting.Code) != PairingCodeDigits {
		t.Fatalf("A 重连后第一条消息 = %+v，want 带新配对码的 %q", waiting, TypeWaiting)
	}
	offerB3 := json.RawMessage(`{"type":"offer","sdp":"v=0 B after A reconnect"}`)
	senderB.send(Message{Type: TypeOffer, SDP: offerB3})
	expectSDP(t, recvB.recv(), TypeOffer, offerB3)
}
