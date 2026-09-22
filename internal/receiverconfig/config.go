package receiverconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// DefaultBufferMS 是抖动缓冲的默认目标深度。实测值：80ms 扛不住 WiFi 突发，
// 600ms 白垫延迟。
const DefaultBufferMS = 150

// Config 是接收端保存在本机的设置。
//
// 它刻意不含 token 本身：token 是长期身份凭据，单独存一个文件，文件权限就是
// 它的边界。这里只存那个文件的路径 —— 路径不是秘密，而"敲 relaymic 就能用"
// 需要它被记住。也不含 STUN/TURN：会话 ICE 由 Hub 在配对后下发，本机再存一份
// 静态配置只会多出一条会和 Hub 冲突的路径。
type Config struct {
	Hub            string  `json:"hub"`
	TokenFile      string  `json:"tokenFile,omitempty"`
	Device         string  `json:"device"`
	ReturnDevice   string  `json:"returnDevice,omitempty"`
	ReturnLoopback string  `json:"returnLoopback,omitempty"`
	BufferMS       int     `json:"bufferMs,omitempty"`
	Gain           float64 `json:"gain,omitempty"`
	NoCGNAT        bool    `json:"noCgnat,omitempty"`
	ForceRelay     bool    `json:"forceRelay,omitempty"`
	TurnTunnel     bool    `json:"turnTunnel,omitempty"`
	DTX            bool    `json:"dtx,omitempty"`
}

// Default 返回一份可以直接使用的默认配置。
func Default(goos string) Config {
	return Config{
		Device:   DefaultOutputDeviceForOS(goos),
		BufferMS: DefaultBufferMS,
	}
}

// Validate 校验用户能改的字段。
//
// Hub 允许为空：接收端还没配置完的时候，控制台也得能起来让人把地址填进去。
func (c Config) Validate() error {
	if c.Hub != "" {
		u, err := url.Parse(c.Hub)
		if err != nil {
			return fmt.Errorf("Hub 地址无法解析: %w", err)
		}
		if u.Scheme != "ws" && u.Scheme != "wss" {
			return fmt.Errorf("Hub 地址必须以 ws:// 或 wss:// 开头，收到 %q", c.Hub)
		}
		if u.Host == "" {
			return fmt.Errorf("Hub 地址缺少主机: %q", c.Hub)
		}
	}
	if strings.TrimSpace(c.Device) == "" {
		return errors.New("输出设备不能为空：不能退回系统默认播放设备")
	}
	if c.BufferMS < 20 || c.BufferMS > 2000 {
		return fmt.Errorf("抖动缓冲必须在 20~2000 毫秒之间，收到 %d", c.BufferMS)
	}
	if c.Gain < 0 || c.Gain > 100 {
		return fmt.Errorf("固定增益必须在 0~100 之间，收到 %v", c.Gain)
	}
	if c.ReturnDevice != "" && c.ReturnLoopback != "" {
		return errors.New("回传只能二选一：虚拟音频设备或系统播放设备环回")
	}
	return nil
}

// Load 读取配置。文件不存在时返回默认配置，让首次运行也能直接进控制台。
func Load(path, goos string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(goos), nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := Default(goos)
	decoder := json.NewDecoder(bytes.NewReader(data))
	// 拼错的键必须直接失败：把 returnDevice 写成 returnDevcie 却静默忽略，
	// 等于上线后才发现回传根本没开。
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("配置 %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置 %s: %w", path, err)
	}
	return cfg, nil
}

// Parse 解析一份送进来的配置，例如本机控制台设置页提交的那份。
//
// 以 base 为底而不是以默认值为底：页面只改了一个字段时，其余字段应该保持
// 原样，而不是被悄悄打回默认值。拼错的键和 Load 一样直接失败。
func Parse(data []byte, base Config) (Config, error) {
	cfg := base
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save 原子写入配置：先写临时文件再改名，避免中途失败留下半份配置。
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// DefaultDir 返回接收端在本机的配置目录。
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".relaymic"
	}
	return filepath.Join(home, ".config", "relaymic")
}

// DefaultPath 返回配置文件的默认路径。
func DefaultPath() string { return filepath.Join(DefaultDir(), "config.json") }

// DefaultPathFor 决定这次用哪份配置：exe 旁边有 config.json 就用它。
//
// 这条规则是为了"把一个文件夹拷给别人"：文件夹里的设置跟着走，而不是去读
// 对方用户目录里那份（那里多半什么都没有）。纯函数，方便测。
func DefaultPathFor(exeDir string) string {
	if beside := filepath.Join(exeDir, "config.json"); exeDir != "" {
		if _, err := os.Stat(beside); err == nil {
			return beside
		}
	}
	return DefaultPath()
}

// DefaultTokenFileFor 找 exe 旁边那份接收端凭据。
//
// 和配置同理：文件夹里的 receiver-token.txt 就是这一份的凭据，用户不必再手填
// 一个绝对路径。
func DefaultTokenFileFor(exeDir string) (string, bool) {
	if exeDir == "" {
		return "", false
	}
	path := filepath.Join(exeDir, "receiver-token.txt")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// ExecutableDir 返回本进程所在目录，取不到时返回空串。
func ExecutableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}
