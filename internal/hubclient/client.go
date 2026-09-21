// Package hubclient 是接收端连公网控制面的那条腿。
//
// 它只做一件事：主动拨出去、保持在线、按控制面的指令把 SDP 交给上层。
// 它不解析 SDP，也不碰音频 —— 音频永远在两端之间直连或走 TURN，
// 控制面只是撮合。
package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hueshu/relaymic/internal/signaling"
)

const (
	defaultDialTimeout  = 15 * time.Second
	defaultReconnectMin = 1 * time.Second
	defaultReconnectMax = 30 * time.Second
)

// DialFunc 允许测试替换拨号实现。
type DialFunc func(ctx context.Context, urlStr string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)

// Config 是客户端配置。
type Config struct {
	// URL 形如 wss://mic.example.com/ws/receiver。
	URL string
	// Token 是这台接收端的长期凭据。它只出现在出站请求头里。
	Token string
	// DialTimeout 是单次拨号的上限。
	DialTimeout time.Duration
	// ReconnectMin / ReconnectMax 是重连退避的上下界。
	ReconnectMin time.Duration
	ReconnectMax time.Duration
	// Logger 留空用 log.Default()。
	Logger *log.Logger
	// Dial 留空用 websocket.Dial。
	Dial DialFunc
}

// Handlers 是控制面消息的落点。除 OnOffer 外都可以留空。
type Handlers struct {
	// OnCode 在 Hub 发来新配对码时调用。
	OnCode func(code string, expiresIn time.Duration)
	// OnJoined 在发送端接入时调用。
	OnJoined func(session string)
	// OnLeft 在发送端离开时调用。
	OnLeft func(session string)
	// OnOffer 收到 offer 后返回 answer。SDP 原样进出，本层不做改写。
	OnOffer func(ctx context.Context, session string, offer json.RawMessage) (json.RawMessage, error)
	// OnError 收到控制面下发的错误或关闭通知。
	OnError func(message string)
}

// Client 维持一条到控制面的长连接。
type Client struct {
	url          string
	token        string
	dialTimeout  time.Duration
	reconnectMin time.Duration
	reconnectMax time.Duration
	logger       *log.Logger
	dial         DialFunc

	writeMu sync.Mutex
}

// New 校验配置并构造客户端。
func New(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, errors.New("hubclient: 缺少控制面地址")
	}
	if cfg.Token == "" {
		return nil, errors.New("hubclient: 缺少接收端凭据")
	}
	c := &Client{
		url:          cfg.URL,
		token:        cfg.Token,
		dialTimeout:  cfg.DialTimeout,
		reconnectMin: cfg.ReconnectMin,
		reconnectMax: cfg.ReconnectMax,
		logger:       cfg.Logger,
		dial:         cfg.Dial,
	}
	if c.dialTimeout <= 0 {
		c.dialTimeout = defaultDialTimeout
	}
	if c.reconnectMin <= 0 {
		c.reconnectMin = defaultReconnectMin
	}
	if c.reconnectMax < c.reconnectMin {
		c.reconnectMax = defaultReconnectMax
	}
	if c.logger == nil {
		c.logger = log.Default()
	}
	if c.dial == nil {
		c.dial = websocket.Dial
	}
	return c, nil
}

// handshakeError 表示控制面在握手阶段就拒绝了这次连接。
//
// 这类错误不会自愈：token 不对、域名不对，重连一万次也是同一个结果。
// 所以它直接结束 Run，把问题暴露给运维，而不是安静地退避重试。
type handshakeError struct {
	url    string
	status int
}

func (e *handshakeError) Error() string {
	return fmt.Sprintf("控制面拒绝连接：%s 返回 %d", e.url, e.status)
}

// Run 持续保持在线，直到 ctx 结束或遇到不会自愈的错误。
func (c *Client) Run(ctx context.Context, h Handlers) error {
	backoff := c.reconnectMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		startedAt := time.Now()
		err := c.session(ctx, h)
		if ctx.Err() != nil {
			return nil
		}

		var refused *handshakeError
		if errors.As(err, &refused) {
			return err
		}
		if err == nil {
			err = errors.New("控制面关闭了连接")
		}
		if time.Since(startedAt) > c.reconnectMax {
			// 这条连接活得够久，说明配置没问题，退避从头开始。
			backoff = c.reconnectMin
		}
		c.logger.Printf("信令连接断开：%v；%s 后重连", err, backoff)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if next := backoff * 2; next < c.reconnectMax {
			backoff = next
		} else {
			backoff = c.reconnectMax
		}
	}
}

func (c *Client) session(ctx context.Context, h Handlers) error {
	conn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(signaling.MaxMessageBytes)

	for {
		var msg signaling.Message
		if err := c.read(ctx, conn, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case signaling.TypeWaiting:
			if h.OnCode != nil {
				h.OnCode(msg.Code, time.Duration(msg.ExpiresIn)*time.Second)
			}
		case signaling.TypeJoined:
			if h.OnJoined != nil {
				h.OnJoined(msg.Session)
			}
		case signaling.TypeLeft:
			if h.OnLeft != nil {
				h.OnLeft(msg.Session)
			}
		case signaling.TypeOffer:
			if h.OnOffer == nil {
				continue
			}
			answer, err := h.OnOffer(ctx, msg.Session, msg.SDP)
			if err != nil {
				c.logger.Printf("协商失败：%v", err)
				continue
			}
			if err := c.write(ctx, conn, signaling.Message{
				Type:    signaling.TypeAnswer,
				Session: msg.Session,
				SDP:     answer,
			}); err != nil {
				return err
			}
		case signaling.TypeError, signaling.TypeClosed:
			if h.OnError != nil {
				h.OnError(msg.Error)
			}
		}
	}
}

func (c *Client) connect(ctx context.Context) (*websocket.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, c.dialTimeout)
	defer cancel()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)
	conn, resp, err := c.dial(dialCtx, c.url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return nil, &handshakeError{url: c.url, status: resp.StatusCode}
		}
		return nil, err
	}
	return conn, nil
}

func (c *Client) write(ctx context.Context, conn *websocket.Conn, msg signaling.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

func (c *Client) read(ctx context.Context, conn *websocket.Conn, out *signaling.Message) error {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText {
		return errors.New("hubclient: 只接受文本消息")
	}
	return json.Unmarshal(data, out)
}
