package signaling

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	// Receivers 是允许连进来的接收端。
	Receivers []ReceiverConfig `json:"receivers"`
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
	return &cfg, nil
}

// Registry 按配置构造配对状态机。token 形状不对会在这里直接失败。
func (c *FileConfig) Registry() (*Registry, error) {
	reg, err := NewRegistry(Config{Receivers: c.Receivers})
	if err != nil {
		return nil, err
	}
	return reg, nil
}

// ErrNoTLS 表示既没给证书、也没允许明文，Hub 无法安全启动。
var ErrNoTLS = errors.New("没有可用的 TLS 证书")
