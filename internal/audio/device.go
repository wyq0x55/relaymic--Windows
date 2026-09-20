// Package audio 负责本机音频设备的枚举与播放。
//
// 接收端只做一件事：把解码后的 PCM 写进指定的输出设备（BlackHole）。
// 它从不打开任何输入设备 —— 回环在结构上就不可能发生。
package audio

import (
	"fmt"

	"github.com/gen2brain/malgo"
	"github.com/hueshu/relaymic/internal/audiodevice"
)

// Device 是一个可用的音频输出设备。
type Device struct {
	Name      string
	ID        malgo.DeviceID
	IsDefault bool
}

// Context 包装 malgo 的初始化，调用方负责 Close。
type Context struct {
	ctx *malgo.AllocatedContext
}

func NewContext() (*Context, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("初始化音频上下文: %w", err)
	}
	return &Context{ctx: ctx}, nil
}

func (c *Context) Close() {
	if c.ctx != nil {
		_ = c.ctx.Uninit()
		c.ctx.Free()
	}
}

// Playbacks 列出所有输出设备。
func (c *Context) Playbacks() ([]Device, error) {
	infos, err := c.ctx.Devices(malgo.Playback)
	if err != nil {
		return nil, fmt.Errorf("枚举输出设备: %w", err)
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

// FindPlayback 按名字子串（忽略大小写和空格）查找输出设备。
// 传入 "blackhole" 就能匹配到 "BlackHole 2ch"。
func (c *Context) FindPlayback(substr string) (Device, error) {
	devices, err := c.Playbacks()
	if err != nil {
		return Device{}, err
	}
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	index, err := audiodevice.UniqueMatchIndex(names, substr)
	if err != nil {
		return Device{}, err
	}
	return devices[index], nil
}
