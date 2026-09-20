package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/hueshu/relaymic/internal/audio"
	"github.com/hueshu/relaymic/internal/receiverconfig"
)

const (
	sampleRate = 48000
	channels   = 2
)

func main() {
	deviceName := flag.String("device", receiverconfig.DefaultOutputDeviceForOS(runtime.GOOS), "输出设备名（子串匹配）；Windows 默认匹配 VB-CABLE")
	tone := flag.Duration("tone", 0, "往目标虚拟音频设备播放这么久的 440Hz 正弦波做自检")
	flag.Parse()

	ctx, err := audio.NewContext()
	if err != nil {
		die(err)
	}
	defer ctx.Close()

	devices, err := ctx.Playbacks()
	if err != nil {
		die(err)
	}
	fmt.Println("输出设备：")
	for _, d := range devices {
		mark := " "
		if d.IsDefault {
			mark = "*"
		}
		fmt.Printf("  %s %s\n", mark, d.Name)
	}

	target, err := ctx.FindPlayback(*deviceName)
	if err != nil {
		die(err)
	}
	fmt.Printf("\n目标设备: %s\n", target.Name)

	if *tone == 0 {
		return
	}

	player, err := ctx.NewPlayer(target, sampleRate, channels, 60)
	if err != nil {
		die(err)
	}
	defer player.Close()

	fmt.Printf("播放 440Hz 正弦波 %s ...\n", *tone)
	deadline := time.Now().Add(*tone)
	phase := 0.0
	step := 2 * math.Pi * 440 / sampleRate
	// 每次写 20ms，模拟真实的 Opus 帧节奏
	frame := make([]int16, sampleRate/50*channels)
	for time.Now().Before(deadline) {
		for i := 0; i < len(frame); i += channels {
			v := int16(math.Sin(phase) * 8000)
			for c := 0; c < channels; c++ {
				frame[i+c] = v
			}
			phase += step
		}
		player.Write(frame)
		time.Sleep(20 * time.Millisecond)
	}

	buffered, dropped, starved := player.Stats()
	fmt.Printf("缓冲=%d 丢弃=%d 欠载=%d\n", buffered, dropped, starved)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
