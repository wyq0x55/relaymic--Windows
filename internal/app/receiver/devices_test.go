package receiver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hueshu/relaymic/internal/audio"
	"github.com/hueshu/relaymic/internal/monitor"
)

func newTestConsole() *console {
	return &console{
		opts:     options{},
		st:       &statusState{state: "启动中"},
		pairing:  monitor.NewPairingState(),
		sessions: monitor.NewSessionState(),
		events:   monitor.NewEventLog(10),
	}
}

// 设置页要按用途分三组候选：写进去的输出、回传采的第二条线、回传环回的播放
// 设备。混在一起会让人选错，而这里的错误不会报错，只会"没声音"。
func TestDeviceInventoryGroupsCandidatesByPurpose(t *testing.T) {
	playback := []audio.Device{
		{Name: "CABLE Input (VB-Audio Virtual Cable)"},
		{Name: "スピーカー (Realtek(R) Audio)", IsDefault: true},
	}
	capture := []audio.Device{{Name: "CABLE Output (VB-Audio Virtual Cable)"}}
	loopback := []audio.Device{{Name: "スピーカー (Realtek(R) Audio)", IsDefault: true}}

	got := newDeviceInventory(playback, capture, loopback)

	if len(got.Output) != 2 || len(got.Return) != 1 || len(got.Loopback) != 1 {
		t.Fatalf("分组不对：output=%d return=%d loopback=%d", len(got.Output), len(got.Return), len(got.Loopback))
	}
	if got.Output[0].Name != playback[0].Name || !got.Output[0].Virtual {
		t.Fatalf("输出组第一条 = %+v，应当是虚拟线", got.Output[0])
	}
	if got.Output[1].Virtual || !got.Output[1].Default {
		t.Fatalf("输出组第二条 = %+v，应当是默认的物理设备", got.Output[1])
	}
	if !got.Return[0].Virtual {
		t.Fatalf("回传组第一条 = %+v，应当是虚拟线", got.Return[0])
	}
}

// 一台设备都没有时给空数组，别给 null：页面里 `for (const d of list)` 遇到
// null 会直接抛错，整个设置页就废了。
func TestDeviceInventoryIsEmptyArraysWhenNothingIsFound(t *testing.T) {
	got := newDeviceInventory(nil, nil, nil)
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(body) != `{"output":[],"return":[],"loopback":[]}` {
		t.Fatalf("空表序列化成 %s", body)
	}
}

// 设备名里有这台机器的硬件信息，和日志一样只给回环看。
func TestDevicesEndpointIsLoopbackOnly(t *testing.T) {
	c := newTestConsole()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7420/api/devices", nil)
	req.RemoteAddr = "192.168.1.9:5000"
	rec := httptest.NewRecorder()

	c.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("局域网来源拿到 %d，want 403", rec.Code)
	}
}

// 音频上下文起不来（没装驱动、被独占）时也要能开设置页：给一份带原因的空表，
// 而不是 500 让页面卡在"扫描中"。
func TestDevicesEndpointExplainsAMissingAudioContext(t *testing.T) {
	c := newTestConsole()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7420/api/devices", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()

	c.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("回环来源拿到 %d，want 200", rec.Code)
	}
	var got deviceInventory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	if got.Error == "" {
		t.Fatalf("音频上下文没起来，却没给出原因：%+v", got)
	}
}
