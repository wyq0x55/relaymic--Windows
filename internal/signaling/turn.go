package signaling

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultTurnCredentialTTL = 10 * time.Minute
	maxTurnCredentialTTL     = time.Hour
)

// TurnIssuer 为每一条配对会话生成 coturn REST API 凭据。共享密钥永远留在 Hub；
// 浏览器和 Receiver 只拿到短期 HMAC 结果。
type TurnIssuer struct {
	urls   []string
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTurnIssuer 校验 coturn REST API 的参数并构造签发器。
func NewTurnIssuer(urls []string, secret []byte, ttl time.Duration) (*TurnIssuer, error) {
	if len(urls) == 0 {
		return nil, errors.New("TURN 至少需要一个地址")
	}
	cleanURLs := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		url := strings.TrimSpace(rawURL)
		if !strings.HasPrefix(strings.ToLower(url), "turn:") && !strings.HasPrefix(strings.ToLower(url), "turns:") {
			return nil, fmt.Errorf("TURN 地址 %q 必须以 turn: 或 turns: 开头", rawURL)
		}
		cleanURLs = append(cleanURLs, url)
	}
	if len(secret) == 0 {
		return nil, errors.New("TURN 共享密钥不能为空")
	}
	if ttl <= 0 || ttl > maxTurnCredentialTTL {
		return nil, fmt.Errorf("TURN 凭据有效期必须在 1 秒到 %s 之间", maxTurnCredentialTTL)
	}
	return &TurnIssuer{
		urls:   cleanURLs,
		secret: append([]byte(nil), secret...),
		ttl:    ttl,
		now:    time.Now,
	}, nil
}

// ICEServers 返回仅对给定 session 有效的一组 coturn REST API 凭据。
func (i *TurnIssuer) ICEServers(sessionID string) []ICEServer {
	if sessionID == "" {
		return nil
	}
	username := fmt.Sprintf("%d:%s", i.now().Add(i.ttl).Unix(), sessionID)
	// coturn 的 REST API 兼容 RFC 5766 的 HMAC-SHA1 派生格式；SHA-1 只用于
	// 与 TURN 协议互操作，不用于 token、配对码或其他安全校验。
	mac := hmac.New(sha1.New, i.secret)
	_, _ = mac.Write([]byte(username))
	credential := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return []ICEServer{{
		URLs:       append([]string(nil), i.urls...),
		Username:   username,
		Credential: credential,
	}}
}
