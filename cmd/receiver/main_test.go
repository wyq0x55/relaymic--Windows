package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadToken(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "token")
	if err := os.WriteFile(good, []byte("  abc123\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	tests := []struct {
		name      string
		token     string
		tokenFile string
		want      string
		wantErr   bool
	}{
		{name: "inline", token: "abc123", want: "abc123"},
		{name: "file trims surrounding whitespace", tokenFile: good, want: "abc123"},
		{name: "both set is ambiguous", token: "abc123", tokenFile: good, wantErr: true},
		{name: "neither set", wantErr: true},
		{name: "empty file", tokenFile: empty, wantErr: true},
		{name: "missing file", tokenFile: filepath.Join(dir, "absent"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readToken(tt.token, tt.tokenFile)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readToken() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readToken() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("readToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveReturnSource(t *testing.T) {
	tests := []struct {
		name       string
		device     string
		loopback   string
		wantOn     bool
		wantLoop   bool
		wantSelect string
		wantErr    bool
	}{
		{name: "都没给就不回传", wantOn: false},
		{name: "第二条虚拟线", device: "CABLE-A Output", wantOn: true, wantSelect: "CABLE-A Output"},
		{name: "环回物理输出", loopback: "ヘッドホン", wantOn: true, wantLoop: true, wantSelect: "ヘッドホン"},
		{name: "两个都给是配置错误", device: "CABLE-A Output", loopback: "ヘッドホン", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveReturnSource(tt.device, tt.loopback)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveReturnSource(%q, %q) = %+v, want error", tt.device, tt.loopback, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveReturnSource() error = %v", err)
			}
			if got.enabled() != tt.wantOn {
				t.Fatalf("enabled() = %v, want %v", got.enabled(), tt.wantOn)
			}
			if got.loopback != tt.wantLoop {
				t.Fatalf("loopback = %v, want %v", got.loopback, tt.wantLoop)
			}
			if got.selector != tt.wantSelect {
				t.Fatalf("selector = %q, want %q", got.selector, tt.wantSelect)
			}
		})
	}
}

// 把回传的采集源指到我们自己正在写的那条线上，得到的不是回传而是啸叫。
func TestReturnSourceMustNotCaptureThePlaybackTarget(t *testing.T) {
	src, err := resolveReturnSource("CABLE Output", "")
	if err != nil {
		t.Fatalf("resolveReturnSource() error = %v", err)
	}
	if err := src.checkNotFeedback("CABLE Output (VB-Audio Virtual Cable)"); err == nil {
		t.Fatal("checkNotFeedback() accepted capturing the device we are writing to")
	}
	if err := src.checkNotFeedback("CABLE Input (VB-Audio Virtual Cable)"); err != nil {
		t.Fatalf("checkNotFeedback() rejected a distinct device: %v", err)
	}
}

func TestDisabledReturnSourceSkipsTheFeedbackCheck(t *testing.T) {
	src, err := resolveReturnSource("", "")
	if err != nil {
		t.Fatalf("resolveReturnSource() error = %v", err)
	}
	if err := src.checkNotFeedback("CABLE Input"); err != nil {
		t.Fatalf("checkNotFeedback() on a disabled source error = %v", err)
	}
}
