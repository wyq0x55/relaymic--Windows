package receiverconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigIsUsable(t *testing.T) {
	cfg := Default("windows")
	if cfg.Device != "cable input" {
		t.Fatalf("Device = %q，want cable input", cfg.Device)
	}
	if cfg.BufferMS != DefaultBufferMS {
		t.Fatalf("BufferMS = %d，want %d", cfg.BufferMS, DefaultBufferMS)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("默认配置应当合法，却报 %v", err)
	}
}

func TestValidateRejectsBadSettings(t *testing.T) {
	base := Default("windows")
	base.Hub = "wss://mic.example.com/ws/receiver"

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"Hub 用了 https", func(c *Config) { c.Hub = "https://mic.example.com/ws/receiver" }},
		{"Hub 缺主机", func(c *Config) { c.Hub = "wss://" }},
		{"设备为空", func(c *Config) { c.Device = "  " }},
		{"缓冲过小", func(c *Config) { c.BufferMS = 5 }},
		{"缓冲过大", func(c *Config) { c.BufferMS = 5000 }},
		{"增益为负", func(c *Config) { c.Gain = -1 }},
		{"两个回传源同时开", func(c *Config) { c.ReturnDevice = "CABLE-A Output"; c.ReturnLoopback = "Speakers" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() 放过了非法配置 %+v", cfg)
			}
		})
	}
}

func TestLoadReturnsDefaultsWhenFileIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := Load(path, "windows")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Device != "cable input" || cfg.BufferMS != DefaultBufferMS {
		t.Fatalf("首次运行应当拿到默认配置，得到 %+v", cfg)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Default("windows")
	want.Hub = "wss://mic.example.com/ws/receiver"
	want.ReturnDevice = "VoiceMeeter Aux Output"
	want.ForceRelay = true
	want.TurnTunnel = true
	want.Gain = 2.5

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(path, "windows")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("往返后配置变了：\n got %+v\nwant %+v", got, want)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("Save() 留下了临时文件")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	// returnDevcie 是 returnDevice 的典型拼错：必须直接失败，不能静默忽略。
	body := `{"hub":"wss://mic.example.com/ws/receiver","device":"cable input","returnDevcie":"CABLE-A Output"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(path, "windows")
	if err == nil {
		t.Fatal("Load() 接受了拼错的键")
	}
	if !strings.Contains(err.Error(), "returnDevcie") {
		t.Fatalf("错误里应当点名拼错的键，得到 %v", err)
	}
}

// 凭据本身永远不进这个文件：存的是它的路径，权限边界还是那个文件。
func TestConfigStoresTheTokenPathNotTheToken(t *testing.T) {
	base := Default("windows")
	base.TokenFile = `C:\relaymic\receiver-token.txt`
	body, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), "tokenFile") {
		t.Fatalf("配置里没有 tokenFile：%s", body)
	}
	// 直接往配置里写 token 这条路不该存在。
	if _, err := Parse([]byte(`{"device":"cable input","token":"secret"}`), base); err == nil {
		t.Fatal("Parse() 接受了写进配置的 token")
	}
}

func TestDefaultPathSitsUnderTheUserConfigDir(t *testing.T) {
	path := DefaultPath()
	if filepath.Base(path) != "config.json" {
		t.Fatalf("DefaultPath() = %q，文件名应当是 config.json", path)
	}
	if filepath.Base(filepath.Dir(path)) != "relaymic" {
		t.Fatalf("DefaultPath() = %q，应当落在 relaymic 目录下", path)
	}
}

func TestParseKeepsFieldsThePageDidNotSend(t *testing.T) {
	base := Default("windows")
	base.Hub = "wss://mic.example.com/ws/receiver"
	base.ReturnDevice = "VoiceMeeter Aux Output"
	base.ForceRelay = true

	got, err := Parse([]byte(`{"device":"cable input"}`), base)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Hub != base.Hub || got.ReturnDevice != base.ReturnDevice || !got.ForceRelay {
		t.Fatalf("只改了一个字段，其余却被改了：\n got %+v\nbase %+v", got, base)
	}
}

func TestParseRoundTripsAFullConfig(t *testing.T) {
	base := Default("windows")
	base.Hub = "wss://mic.example.com/ws/receiver"
	base.ReturnLoopback = "Speakers"
	base.BufferMS = 220
	base.Gain = 1.5
	base.NoCGNAT = true
	base.TurnTunnel = true
	base.DTX = true

	body, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := Parse(body, Default("windows"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got != base {
		t.Fatalf("往返后配置变了：\n got %+v\nwant %+v", got, base)
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte(`{"device":"cable input","returnDevcie":"CABLE-A Output"}`), Default("windows"))
	if err == nil {
		t.Fatal("Parse() 接受了拼错的键")
	}
	if !strings.Contains(err.Error(), "returnDevcie") {
		t.Fatalf("错误里应当点名拼错的键，得到 %v", err)
	}
}

func TestParseRejectsValuesOutOfRange(t *testing.T) {
	_, err := Parse([]byte(`{"device":"cable input","bufferMs":5}`), Default("windows"))
	if err == nil {
		t.Fatal("Parse() 放过了越界的抖动缓冲")
	}
}

func TestParseRejectsAConfigWithNoOutputDevice(t *testing.T) {
	_, err := Parse([]byte(`{"device":"   "}`), Default("windows"))
	if err == nil {
		t.Fatal("Parse() 放过了空的输出设备")
	}
}

// 把整个文件夹拷给别人时，设置得跟着走：exe 旁边有 config.json 就用它，
// 而不是去读对方用户目录里的那份。
func TestDefaultPathForPrefersAConfigBesideTheExecutable(t *testing.T) {
	dir := t.TempDir()
	if got := DefaultPathFor(dir); got != DefaultPath() {
		t.Fatalf("旁边没有 config.json 时用了 %q，应当回落到用户目录", got)
	}

	beside := filepath.Join(dir, "config.json")
	if err := os.WriteFile(beside, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if got := DefaultPathFor(dir); got != beside {
		t.Fatalf("DefaultPathFor() = %q，want %q", got, beside)
	}
}

// 凭据同理：文件夹里的 receiver-token.txt 就是这份的凭据，不用手填路径。
func TestDefaultTokenFileForFindsTheTokenBesideTheExecutable(t *testing.T) {
	dir := t.TempDir()
	if _, ok := DefaultTokenFileFor(dir); ok {
		t.Fatal("旁边没有 token 文件时不该报「有」")
	}

	token := filepath.Join(dir, "receiver-token.txt")
	if err := os.WriteFile(token, []byte("abc\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	got, ok := DefaultTokenFileFor(dir)
	if !ok || got != token {
		t.Fatalf("DefaultTokenFileFor() = %q, %v，want %q, true", got, ok, token)
	}
}
