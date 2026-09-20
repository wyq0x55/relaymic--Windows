package audio

import (
	"fmt"
	"strings"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/hueshu/relaymic/internal/audiodevice"
)

// 采集能力只给发送端用。接收端"从不打开输入设备"的防回环约定不变：
// 它们是两个进程，接收端的代码路径里没有任何指向这里的调用。

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

// FindCapture 按名字子串查找输入设备。空串返回系统默认。
func (c *Context) FindCapture(substr string) (Device, error) {
	devices, err := c.Captures()
	if err != nil {
		return Device{}, err
	}
	if substr == "" {
		for _, d := range devices {
			if d.IsDefault {
				return d, nil
			}
		}
		if len(devices) > 0 {
			return devices[0], nil
		}
		return Device{}, fmt.Errorf("这台机器上没有输入设备")
	}
	want := audiodevice.NormalizeName(substr)
	for _, d := range devices {
		if strings.Contains(audiodevice.NormalizeName(d.Name), want) {
			return d, nil
		}
	}
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	return Device{}, fmt.Errorf("没有找到名字含 %q 的输入设备，当前可用：%s", substr, strings.Join(names, " / "))
}

// Capturer 持续从一个输入设备读 PCM。
type Capturer struct {
	device *malgo.Device
}

// NewCapturer 在 dev 上开一个采集流，每来一段交错 PCM 就调一次 onPCM。
// onPCM 运行在实时音频回调里：不许阻塞、不许分配大对象，拷走数据就返回。
func (c *Context) NewCapturer(dev Device, sampleRate, channels int, onPCM func([]int16)) (*Capturer, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = uint32(channels)
	cfg.Capture.DeviceID = dev.ID.Pointer()
	cfg.SampleRate = uint32(sampleRate)

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
			return nil, fmt.Errorf("打开输入设备 %q: %w", dev.Name, r.err)
		}
		if err := r.device.Start(); err != nil {
			r.device.Uninit()
			return nil, fmt.Errorf("启动输入设备 %q: %w", dev.Name, err)
		}
		return &Capturer{device: r.device}, nil
	case <-time.After(OpenTimeout):
		return nil, fmt.Errorf("打开输入设备 %q 超过 %s 无响应", dev.Name, OpenTimeout)
	}
}

func (c *Capturer) Close() {
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
}
