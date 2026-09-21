package signaling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "signaling.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadFileReadsConfig(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	path := writeConfig(t, `{
  "listen": ":443",
  "certFile": "/etc/ssl/mic.example.com/fullchain.pem",
  "keyFile": "/etc/ssl/mic.example.com/privkey.pem",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [{"urls": ["stun:stun.example.com:3478"]}],
  "receivers": [{"name": "公司电脑", "token": "`+token+`"}]
}`)

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if cfg.Listen != ":443" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "mic.example.com" {
		t.Fatalf("allowedOrigins = %v", cfg.AllowedOrigins)
	}
	if len(cfg.ICEServers) != 1 || cfg.ICEServers[0].URLs[0] != "stun:stun.example.com:3478" {
		t.Fatalf("iceServers = %+v", cfg.ICEServers)
	}
	if len(cfg.Receivers) != 1 || cfg.Receivers[0].Name != "公司电脑" {
		t.Fatalf("receivers = %+v", cfg.Receivers)
	}

	reg, err := cfg.Registry()
	if err != nil {
		t.Fatalf("Registry() error = %v", err)
	}
	if _, ok := reg.Authenticate(token); !ok {
		t.Fatal("Registry() did not accept the configured token")
	}
}

func TestLoadFileRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, `{"listen": ":443", "receiver": []}`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted a misspelled field; a typo here silently disables configuration")
	}
}

func TestLoadFileRejectsEmptyReceiverList(t *testing.T) {
	path := writeConfig(t, `{"listen": ":443", "receivers": []}`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted a config with no receivers")
	}
}

func TestLoadFileRejectsBadTokenShape(t *testing.T) {
	path := writeConfig(t, `{"listen": ":443", "receivers": [{"name": "公司电脑", "token": "short"}]}`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if _, err := cfg.Registry(); err == nil {
		t.Fatal("Registry() accepted a token that is not 256 bits")
	}
}

func TestLoadFileRejectsLongLivedTURNCredentialsInPublicICEConfig(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	path := writeConfig(t, `{
  "listen": ":443",
  "iceServers": [{
    "urls": ["turn:turn.example.com:3478"],
    "username": "long-lived-user",
    "credential": "long-lived-password"
  }],
  "receivers": [{"name": "company-windows", "token": "`+token+`"}]
}`)
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile() accepted public long-lived TURN credentials")
	}
}

func TestLoadFileReportsMissingFile(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil {
		t.Fatal("LoadFile() accepted a missing file")
	}
	if !strings.Contains(err.Error(), "absent.json") {
		t.Fatalf("error = %v, want it to name the missing path", err)
	}
}
