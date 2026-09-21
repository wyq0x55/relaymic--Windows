package audio

import (
	"fmt"
	"time"

	"github.com/gen2brain/malgo"
)

// 采集用于两处：发送端采本机麦克风；接收端在显式打开回传时采第二条虚拟线、
// 或对某个播放设备做环回。
//
// 设备一律由调用方显式指定，选择规则与播放侧共用同一套 fail-closed 判定。

// Captures 列出所有输入设备。
func (c *Context) Captures() ([]Device, error) {
	infos, err := c.ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("枚举输入设备: %w", err)
	}
	out := make([]Device, 0, len(infos))
	for _, info := range infos {
		out = append(out, Device{
			Name:      info.Name(),
			ID:        info.ID,
			IsDefault: info.IsDefault != 0,
		})
	}
	return out, nil
}

// FindCapture 按名字子串查找输入设备。
//
// 规则和播放侧完全一致：空串、无匹配、多个匹配都是错误。
func (c *Context) FindCapture(substr string) (Device, error) {
	devices, err := c.Captures()
	if err != nil {
		return Device{}, err
	}
	return SelectDevice(devices, substr)
}

// Capturer 持续从一个输入设备读 PCM。
type Capturer struct {
	device *malgo.Device
}

// NewCapturer 在 dev 这个录制设备上开一个采集流。
func (c *Context) NewCapturer(dev Device, sampleRate, channels int, onPCM func([]int16)) (*Capturer, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = uint32(channels)
	cfg.Capture.DeviceID = dev.ID.Pointer()
	cfg.SampleRate = uint32(sampleRate)
	return c.startCapture(cfg, "输入设备", dev.Name, channels, onPCM)
}

// NewLoopbackCapturer 采集 dev 这个播放设备正在播放的内容。
//
// WASAPI 环回：设备 ID 填的是播放设备，不是录制设备。代价是它拿到这台机器上
// 所有系统声音，不只目标应用 —— 能装第二条虚拟线时优先用第二条线。
func (c *Context) NewLoopbackCapturer(dev Device, sampleRate, channels int, onPCM func([]int16)) (*Capturer, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Loopback)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = uint32(channels)
	cfg.Capture.DeviceID = dev.ID.Pointer()
	cfg.SampleRate = uint32(sampleRate)
	return c.startCapture(cfg, "环回设备", dev.Name, channels, onPCM)
}

// startCapture 收拢"建流 → InitDevice → Start"这段共用逻辑。
// onPCM 运行在实时音频回调里：不许阻塞、不许分配大对象，拷走数据就返回。
func (c *Context) startCapture(cfg malgo.DeviceConfig, kind, name string, channels int, onPCM func([]int16)) (*Capturer, error) {
	// 回调给的是字节流，转成 int16 后交出去。
	// 复用同一块 slice：onPCM 的约定就是"用完即弃、要留就拷"。
	var pcm []int16

	type result struct {
		device *malgo.Device
		err    error
	}
	// InitDevice 是阻塞的 cgo 调用，可能因驱动残留而永久挂起，
	// 和播放侧同样的问题、同样的对策：超时就放弃。
	done := make(chan result, 1)
	go func() {
		device, err := malgo.InitDevice(c.ctx.Context, cfg, malgo.DeviceCallbacks{
			Data: func(_, in []byte, frameCount uint32) {
				n := int(frameCount) * channels
				if cap(pcm) < n {
					pcm = make([]int16, n)
				}
				pcm = pcm[:n]
				for i := 0; i < n; i++ {
					pcm[i] = int16(uint16(in[i*2]) | uint16(in[i*2+1])<<8)
				}
				onPCM(pcm)
			},
		})
		done <- result{device, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("打开%s %q: %w", kind, name, r.err)
		}
		if err := r.device.Start(); err != nil {
			r.device.Uninit()
			return nil, fmt.Errorf("启动%s %q: %w", kind, name, err)
		}
		return &Capturer{device: r.device}, nil
	case <-time.After(OpenTimeout):
		return nil, fmt.Errorf("打开%s %q 超过 %s 无响应", kind, name, OpenTimeout)
	}
}

func (c *Capturer) Close() {
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
}
