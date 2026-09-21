// Package audio 负责本机音频设备的枚举、播放与采集。
//
// 接收端默认只往指定输出设备写 PCM。只有显式打开回传时才会采集，
// 而且采集源必须和播放目标分开 —— 采自己刚写进去的那条线是环，不是回传。
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
	return SelectDevice(devices, substr)
}

// SelectDevice 按名字子串（忽略大小写与空格）挑设备。
//
// 空 selector、无匹配、多个匹配都是错误，播放/采集/环回一视同仁。
// 默认设备是系统属性，不是"选不到就随便挑一个"的许可：回传路径尤其不能这样，
// 挑错设备等于把整台机器的系统声音送进会议。
func SelectDevice(devices []Device, selector string) (Device, error) {
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	index, err := audiodevice.UniqueMatchIndex(names, selector)
	if err != nil {
		return Device{}, err
	}
	return devices[index], nil
}

// Loopbacks 列出可以做环回采集的设备。
//
// WASAPI 的环回是"采集某个播放设备正在播放的内容"，所以候选就是播放设备列表 ——
// miniaudio 的 Devices(Loopback) 返回的正是这一份，不是录制设备那一份。
func (c *Context) Loopbacks() ([]Device, error) {
	infos, err := c.ctx.Devices(malgo.Loopback)
	if err != nil {
		return nil, fmt.Errorf("枚举环回设备: %w", err)
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

// FindLoopback 按名字子串挑一个可环回的播放设备。
func (c *Context) FindLoopback(substr string) (Device, error) {
	devices, err := c.Loopbacks()
	if err != nil {
		return Device{}, err
	}
	return SelectDevice(devices, substr)
}
