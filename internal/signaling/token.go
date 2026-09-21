package signaling

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

const (
	// TokenBytes 是接收端 Bearer token 的熵：256 位，穷举不可行。
	TokenBytes = 32
	// TokenDigestBytes 是 Hub 保存的摘要长度。
	TokenDigestBytes = sha256.Size
)

// TokenDigest 是 token 的 SHA-256 摘要。Hub 只保存它，不保存 token 本身，
// 于是配置文件泄漏或进程被读走内存也不会直接交出可用凭据。
type TokenDigest [TokenDigestBytes]byte

// NewToken 生成一个接收端 token，编码为 43 字符的 raw url-safe base64。
func NewToken() (string, error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成 token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DigestToken 把 token 收敛成定长摘要。
func DigestToken(token string) TokenDigest { return sha256.Sum256([]byte(token)) }

// TokenMatches 常数时间比较摘要与待验证 token。
func TokenMatches(digest TokenDigest, presented string) bool {
	if presented == "" {
		return false
	}
	want := DigestToken(presented)
	return subtle.ConstantTimeCompare(digest[:], want[:]) == 1
}

// ParseTokenDigest 校验配置里 token 的形状，并立刻转成摘要。
// 形状不对就在启动时报错：配置写错不该等到有人连上来才暴露。
func ParseTokenDigest(token string) (TokenDigest, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return TokenDigest{}, fmt.Errorf("token 必须是 raw url-safe base64: %w", err)
	}
	if len(raw) != TokenBytes {
		return TokenDigest{}, fmt.Errorf("token 解码后必须是 %d 字节，实际 %d 字节", TokenBytes, len(raw))
	}
	return DigestToken(token), nil
}
