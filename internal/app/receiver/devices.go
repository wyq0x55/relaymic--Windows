package receiver

import (
	"encoding/json"
	"net/http"

	"github.com/hueshu/relaymic/internal/audio"
	"github.com/hueshu/relaymic/internal/audiodevice"
	"github.com/hueshu/relaymic/internal/monitor"
)

// deviceOption 是设置页下拉框里的一条设备。
type deviceOption struct {
	Name string `json:"name"`
	// Default 是系统默认设备。它只是提示，不是"选不到就用它"的许可。
	Default bool `json:"default"`
	// Virtual 是虚拟声卡。两条音频路径都该落在虚拟线上。
	Virtual bool `json:"virtual"`
}

// deviceInventory 是设置页能选的全部候选，按用途分组。
//
// 分三组而不是给一张大表：这三处要的设备根本不是同一类 —— 写进去的是播放
// 设备，回传采的是录制设备，环回采的又是播放设备。混在一起让人选错，而这里的
// 错误不报错，只会"没声音"。
type deviceInventory struct {
	Output   []deviceOption `json:"output"`
	Return   []deviceOption `json:"return"`
	Loopback []deviceOption `json:"loopback"`
	Error    string         `json:"error,omitempty"`
}

func newDeviceInventory(playback, capture, loopback []audio.Device) deviceInventory {
	return deviceInventory{
		Output:   deviceOptions(playback),
		Return:   deviceOptions(capture),
		Loopback: deviceOptions(loopback),
	}
}

// deviceOptions 保证返回空数组而不是 nil：页面里 for (const d of list) 遇到
// null 会直接抛错，整个设置页就废了。
func deviceOptions(devices []audio.Device) []deviceOption {
	out := make([]deviceOption, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceOption{
			Name:    d.Name,
			Default: d.IsDefault,
			Virtual: audiodevice.IsVirtualCable(d.Name),
		})
	}
	return out
}

// scanDevices 现扫一遍本机设备。每次请求都重扫：插拔虚拟声卡之后不用重启
// 接收端，页面上点一下"重新扫描"就能看到。
func (c *console) scanDevices() deviceInventory {
	if c.actx == nil {
		// 音频上下文起不来（驱动缺失、设备被独占）也要能开设置页，所以给的是
		// 一份带原因的空表，而不是 500。
		return deviceInventory{
			Output:   []deviceOption{},
			Return:   []deviceOption{},
			Loopback: []deviceOption{},
			Error:    "音频没初始化成功，暂时扫不到设备",
		}
	}
	playback, err := c.actx.Playbacks()
	if err != nil {
		return deviceInventory{Output: []deviceOption{}, Return: []deviceOption{}, Loopback: []deviceOption{}, Error: err.Error()}
	}
	capture, err := c.actx.Captures()
	if err != nil {
		return deviceInventory{Output: []deviceOption{}, Return: []deviceOption{}, Loopback: []deviceOption{}, Error: err.Error()}
	}
	loopback, err := c.actx.Loopbacks()
	if err != nil {
		return deviceInventory{Output: []deviceOption{}, Return: []deviceOption{}, Loopback: []deviceOption{}, Error: err.Error()}
	}
	return newDeviceInventory(playback, capture, loopback)
}

// serveDevices 把本机能选的音频设备交给设置页。
//
// 只给回环看：设备名里带着这台机器的硬件信息，和日志一个道理。
func (c *console) serveDevices(w http.ResponseWriter, r *http.Request) {
	if !monitor.IsLoopbackRemoteAddr(r.RemoteAddr) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.scanDevices())
}
