package signaling

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// FileConfig 是 Hub 的配置文件形态。
//
// 它只有一份：公网入口地址、TLS 证书、允许的来源、ICE 配置、接收端清单。
// 没有数据库、没有管理 API —— 改配置就是改文件再重启，少一个能被远程写坏的面。
type FileConfig struct {
	// Listen 是监听地址，生产上是 ":443"。
	Listen string `json:"listen"`
	// CertFile / KeyFile 是 PEM 证书链与私钥；本地自测可以留空改用自签。
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	// AllowedOrigins 是允许发起 WebSocket 的 Origin host 白名单。
	AllowedOrigins []string `json:"allowedOrigins"`
	// ICEServers 通过 HTTPS 发给浏览器；#3 接上 TURN 后填在这里。
	ICEServers []ICEServer `json:"iceServers"`
	// Turn 是 coturn 的短期凭据签发配置。共享密钥只从文件读取，绝不出现在
	// 可被浏览器读取的 iceServers 或 JSON 配置里。
	Turn *TurnConfig `json:"turn,omitempty"`
	// Receivers 是允许连进来的接收端。
	Receivers []ReceiverConfig `json:"receivers"`
}

// TurnConfig 是 Hub 读取 coturn REST API 共享密钥所需的最小配置。
type TurnConfig struct {
	URLs                 []string `json:"urls"`
	AuthSecretFile       string   `json:"authSecretFile"`
	CredentialTTLSeconds int      `json:"credentialTTLSeconds,omitempty"`
	// TunnelTarget 是隧道端点要转发到的本机地址，例如 127.0.0.1:3478。
	// 留空表示不开放隧道；接收端只走 HTTP 代理时靠它把 TURN/TCP 送出网。
	TunnelTarget string `json:"tunnelTarget,omitempty"`
}

// LoadFile 读取并做结构校验。
func LoadFile(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// 拼错的键必须报错：写成 "receiver" 而本意是 "receivers"，
	// 静默忽略等于上线后才发现一台机器都连不上。
	dec.DisallowUnknownFields()
	var cfg FileConfig
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	if cfg.Listen == "" {
		return nil, fmt.Errorf("配置 %s: listen 不能为空", path)
	}
	if len(cfg.Receivers) == 0 {
		return nil, fmt.Errorf("配置 %s: 至少要配一台接收端", path)
	}
	if err := validatePublicICEServers(cfg.ICEServers); err != nil {
		return nil, fmt.Errorf("配置 %s: %w", path, err)
	}
	return &cfg, nil
}

func validatePublicICEServers(servers []ICEServer) error {
	for _, server := range servers {
		if len(server.URLs) == 0 {
			return errors.New("iceServers 不能含空地址")
		}
		if server.Username != "" || server.Credential != "" {
			return errors.New("公开 iceServers 不能含凭据；TURN 必须使用 turn.authSecretFile")
		}
		for _, rawURL := range server.URLs {
			url := strings.TrimSpace(rawURL)
			if url == "" {
				return errors.New("iceServers 不能含空地址")
			}
			if strings.HasPrefix(strings.ToLower(url), "turn:") || strings.HasPrefix(strings.ToLower(url), "turns:") {
				return errors.New("公开 iceServers 不能配置 TURN；请使用 turn.authSecretFile")
			}
		}
	}
	return nil
}

// Registry 按配置构造配对状态机。token 形状不对会在这里直接失败。
func (c *FileConfig) Registry() (*Registry, error) {
	reg, err := NewRegistry(Config{Receivers: c.Receivers})
	if err != nil {
		return nil, err
	}
	return reg, nil
}

// TurnIssuer 从受限文件读取 coturn REST API 共享密钥。未配置 TURN 时返回 nil。
func (c *FileConfig) TurnIssuer() (*TurnIssuer, error) {
	if c.Turn == nil {
		return nil, nil
	}
	if c.Turn.AuthSecretFile == "" {
		return nil, errors.New("turn.authSecretFile 不能为空")
	}
	secret, err := os.ReadFile(c.Turn.AuthSecretFile)
	if err != nil {
		return nil, fmt.Errorf("读取 TURN 共享密钥: %w", err)
	}
	ttl := defaultTurnCredentialTTL
	if c.Turn.CredentialTTLSeconds != 0 {
		ttl = time.Duration(c.Turn.CredentialTTLSeconds) * time.Second
	}
	return NewTurnIssuer(c.Turn.URLs, []byte(strings.TrimSpace(string(secret))), ttl)
}

// TunnelTarget 返回 TURN 隧道要转发的本机地址；留空表示不开放隧道端点。
func (c *FileConfig) TunnelTarget() string {
	if c.Turn == nil {
		return ""
	}
	return strings.TrimSpace(c.Turn.TunnelTarget)
}

// ErrNoTLS 表示既没给证书、也没允许明文，Hub 无法安全启动。
var ErrNoTLS = errors.New("没有可用的 TLS 证书")
