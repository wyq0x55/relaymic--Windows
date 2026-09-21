package signaling

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// defaultWriteTimeout 是单条控制面消息的写入上限。
// 对端卡住时不能让 Hub 的转发协程一起卡住。
const defaultWriteTimeout = 5 * time.Second

// ServerConfig 是 Hub 的构造参数。
type ServerConfig struct {
	// Registry 提供 token 校验与配对码，必填。
	Registry *Registry
	// ICEServers 通过 HTTPS 发给浏览器；#3 接上 TURN 后填在这里。
	ICEServers []ICEServer
	// TurnIssuer 为成功配对的双方签发同一组短期 coturn REST API 凭据。
	TurnIssuer *TurnIssuer
	// Page 是发送端页面。为空则根路径返回 404。
	Page []byte
	// AllowedOrigins 是允许发起 WebSocket 的 Origin host 白名单。
	// 留空表示只允许同源（Origin host 必须等于请求 Host）。
	AllowedOrigins []string
	// MaxPairingAttemptsPerConn 覆盖默认的失败次数上限。
	MaxPairingAttemptsPerConn int
	// WriteTimeout 覆盖默认写超时。
	WriteTimeout time.Duration
	// Logger 接收运行日志。留空用 log.Default()。
	Logger *log.Logger
}

// Server 是公网控制面：一条 HTTPS/WSS 443 入口，两边都主动连进来。
//
// 它只做三件事：认证接收端、用配对码把发送端接到某台接收端上、原样转发 SDP。
// 音频永远不经过这里。
type Server struct {
	registry    *Registry
	ice         []ICEServer
	turnIssuer  *TurnIssuer
	page        []byte
	origins     []string
	maxAttempts int
	writeTime   time.Duration
	logger      *log.Logger

	mu        sync.Mutex
	receivers map[string]*receiverConn
	sessions  map[string]*session
}

type receiverConn struct {
	id   string
	name string
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *receiverConn) write(ctx context.Context, msg Message) error {
	return writeMessage(ctx, c.conn, &c.mu, msg)
}

type senderConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *senderConn) write(ctx context.Context, msg Message) error {
	return writeMessage(ctx, c.conn, &c.mu, msg)
}

type session struct {
	id       string
	receiver *receiverConn
	sender   *senderConn
}

// NewServer 构造 Hub。
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Registry == nil {
		return nil, errors.New("signaling: ServerConfig.Registry 不能为空")
	}
	s := &Server{
		registry:    cfg.Registry,
		ice:         cfg.ICEServers,
		turnIssuer:  cfg.TurnIssuer,
		page:        cfg.Page,
		origins:     cfg.AllowedOrigins,
		maxAttempts: cfg.MaxPairingAttemptsPerConn,
		writeTime:   cfg.WriteTimeout,
		logger:      cfg.Logger,
		receivers:   make(map[string]*receiverConn),
		sessions:    make(map[string]*session),
	}
	if s.maxAttempts <= 0 {
		s.maxAttempts = MaxPairingAttemptsPerConn
	}
	if s.writeTime <= 0 {
		s.writeTime = defaultWriteTimeout
	}
	if s.logger == nil {
		s.logger = log.Default()
	}
	return s, nil
}

func (s *Server) sessionICEServers(sessionID string) []ICEServer {
	servers := append([]ICEServer(nil), s.ice...)
	if s.turnIssuer != nil {
		servers = append(servers, s.turnIssuer.ICEServers(sessionID)...)
	}
	return servers
}

// Handler 返回控制面的 HTTP 路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ice", s.handleICE)
	mux.HandleFunc("GET /ws/receiver", s.handleReceiver)
	mux.HandleFunc("GET /ws/sender", s.handleSender)
	mux.HandleFunc("GET /", s.handlePage)
	return mux
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || len(s.page) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 页面是唯一入口，禁掉嗅探与内嵌，减少被当成跳板的面。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(s.page)
}

func (s *Server) handleICE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// 不设 CORS 头：这个配置只给同源页面用，别处拿不到。
	_ = json.NewEncoder(w).Encode(map[string]any{"iceServers": s.ice})
}

func (s *Server) handleReceiver(w http.ResponseWriter, r *http.Request) {
	receiver, ok := s.registry.Authenticate(bearerToken(r))
	if !ok {
		// 不区分"没带 token"和"token 不对"。
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// 接收端不是浏览器，没有 Origin；它的身份由 Bearer token 证明。
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(MaxMessageBytes)
	s.runReceiver(r.Context(), conn, receiver)
}

func (s *Server) handleSender(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(MaxMessageBytes)
	s.runSender(r.Context(), conn, r)
}

// originAllowed 判断浏览器来源。
//
// 浏览器发 WebSocket 一定带 Origin；没带 Origin 的客户端不是我们支持的发送端，
// 直接拒绝 —— 否则任何本地脚本都能拿配对码去试。
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if len(s.origins) == 0 {
		return strings.EqualFold(u.Host, r.Host)
	}
	for _, allowed := range s.origins {
		if strings.EqualFold(allowed, u.Host) {
			return true
		}
	}
	return false
}

func (s *Server) runReceiver(ctx context.Context, conn *websocket.Conn, receiver *Receiver) {
	rc := &receiverConn{id: receiver.ID(), name: receiver.Name(), conn: conn}

	s.mu.Lock()
	previous := s.receivers[rc.id]
	s.receivers[rc.id] = rc
	s.mu.Unlock()
	if previous != nil {
		// 同一台机器重连：旧连接必须让位，否则两个房间会抢同一台接收端。
		//
		// 这里用 CloseNow 而不是 Close：Close 会等对端的关闭帧，
		// 而旧连接此刻正堵在 Read 上，双方互等会白等一个超时。
		_ = previous.conn.CloseNow()
	}
	s.logger.Printf("接收端已连接: %s", rc.name)
	defer func() {
		s.dropReceiver(rc)
		s.logger.Printf("接收端已断开: %s", rc.name)
	}()

	if err := s.issueCode(ctx, rc); err != nil {
		return
	}

	for {
		var msg Message
		if err := readMessage(ctx, conn, &msg); err != nil {
			return
		}
		if msg.Type != TypeAnswer {
			// 接收端只允许发 answer。多一条通道就多一个需要审计的转发路径。
			_ = conn.Close(websocket.StatusPolicyViolation, "unexpected message type")
			return
		}
		s.routeAnswer(rc, msg)
	}
}

func (s *Server) runSender(ctx context.Context, conn *websocket.Conn, r *http.Request) {
	sc := &senderConn{conn: conn}

	var msg Message
	if err := readMessage(ctx, conn, &msg); err != nil {
		return
	}

	receiver, err := s.pair(ctx, sc, r, &msg)
	if err != nil {
		return
	}

	sessionID, err := newSessionID()
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "session id")
		return
	}

	s.mu.Lock()
	rc := s.receivers[receiver.ID()]
	if rc == nil {
		s.mu.Unlock()
		// 码刚兑换完接收端就断了：这次配对算作废，等它回来重发码。
		_ = sc.write(ctx, Message{Type: TypeError, Error: ReceiverOfflineText})
		return
	}
	sess := &session{id: sessionID, receiver: rc, sender: sc}
	s.sessions[sessionID] = sess
	s.mu.Unlock()
	iceServers := s.sessionICEServers(sessionID)

	if err := sc.write(ctx, Message{Type: TypePaired, Session: sessionID, Name: receiver.Name(), ICEServers: iceServers}); err != nil {
		s.endSession(sess)
		return
	}
	_ = rc.write(ctx, Message{Type: TypeJoined, Session: sessionID, ICEServers: iceServers})

	defer s.endSession(sess)
	for {
		var next Message
		if err := readMessage(ctx, conn, &next); err != nil {
			return
		}
		switch next.Type {
		case TypeOffer:
			if len(next.SDP) == 0 {
				continue
			}
			if err := rc.write(ctx, Message{Type: TypeOffer, Session: sessionID, SDP: next.SDP}); err != nil {
				return
			}
		default:
			_ = conn.Close(websocket.StatusPolicyViolation, "unexpected message type")
			return
		}
	}
}

// pair 反复读 pair 消息直到成功、超限或对端离开。
func (s *Server) pair(ctx context.Context, sc *senderConn, r *http.Request, msg *Message) (*Receiver, error) {
	client := clientKey(r)
	for attempt := 1; ; attempt++ {
		var receiver *Receiver
		if msg.Type == TypePair {
			code, err := NormalizePairingCode(msg.Code)
			if err == nil {
				receiver, err = s.registry.Redeem(client, code)
			}
			if err == nil {
				return receiver, nil
			}
		}
		if msg.Type != TypePair {
			_ = sc.conn.Close(websocket.StatusPolicyViolation, "expected pair")
			return nil, errors.New("signaling: 首条消息不是 pair")
		}
		_ = sc.write(ctx, Message{Type: TypeError, Error: PairingFailedText})
		if attempt >= s.maxAttempts {
			_ = sc.conn.Close(websocket.StatusPolicyViolation, "too many pairing attempts")
			return nil, errors.New("signaling: 配对尝试超限")
		}
		if err := readMessage(ctx, sc.conn, msg); err != nil {
			return nil, err
		}
	}
}

func (s *Server) routeAnswer(rc *receiverConn, msg Message) {
	s.mu.Lock()
	sess := s.sessions[msg.Session]
	if sess == nil || sess.receiver != rc {
		s.mu.Unlock()
		return
	}
	sender := sess.sender
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), s.writeTime)
	defer cancel()
	_ = sender.write(ctx, Message{Type: TypeAnswer, Session: sess.id, SDP: msg.SDP})
}

// endSession 收尾一条会话：通知接收端发送端走了，并给它一个新码。
func (s *Server) endSession(sess *session) {
	s.mu.Lock()
	if s.sessions[sess.id] == sess {
		delete(s.sessions, sess.id)
	}
	current := s.receivers[sess.receiver.id]
	s.mu.Unlock()

	if current != sess.receiver {
		// 接收端已经换了一条连接，新连接自己会拿到码。
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.writeTime)
	defer cancel()
	_ = sess.receiver.write(ctx, Message{Type: TypeLeft, Session: sess.id})
	_ = s.issueCode(ctx, sess.receiver)
}

// dropReceiver 处理接收端断开：作废码、关掉它的全部会话、通知发送端。
func (s *Server) dropReceiver(rc *receiverConn) {
	s.mu.Lock()
	// 只有"当前这条连接"断开才作废配对码。被新连接顶掉的旧连接
	// 走同一个 defer，如果它也去 Release，就会把新连接的码一起废掉。
	isCurrent := s.receivers[rc.id] == rc
	if isCurrent {
		delete(s.receivers, rc.id)
	}
	var orphaned []*session
	for id, sess := range s.sessions {
		if sess.receiver == rc && isCurrent {
			orphaned = append(orphaned, sess)
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()

	if !isCurrent {
		return
	}
	s.registry.Release(rc.id)

	ctx, cancel := context.WithTimeout(context.Background(), s.writeTime)
	defer cancel()
	for _, sess := range orphaned {
		_ = sess.sender.write(ctx, Message{Type: TypeClosed, Session: sess.id, Error: ReceiverOfflineText})
		_ = sess.sender.conn.Close(websocket.StatusGoingAway, "receiver offline")
	}
}

func (s *Server) issueCode(ctx context.Context, rc *receiverConn) error {
	code, _, err := s.registry.IssueCode(rc.id)
	if err != nil {
		return err
	}
	return rc.write(ctx, Message{
		Type:      TypeWaiting,
		Code:      string(code),
		ExpiresIn: int(s.registry.CodeTTL().Seconds()),
	})
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// clientKey 是限流用的客户端标识。这里用直连地址：Hub 部署在反向代理后面时
// 需要由代理负责限流，别把 X-Forwarded-For 当成可信输入。
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func newSessionID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成会话标识: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func writeMessage(ctx context.Context, conn *websocket.Conn, mu *sync.Mutex, msg Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

func readMessage(ctx context.Context, conn *websocket.Conn, out *Message) error {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return errors.New("signaling: 只接受文本消息")
	}
	return json.Unmarshal(data, out)
}
