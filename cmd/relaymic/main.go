// relaymic 是 RelayMic 的唯一入口。
//
// 敲 relaymic 就是起本机应用：接收端加上本机控制台页面。要别的能力再加子命令，
// 用法见 relaymic help。旧名字（relaymic-receiver 等）都还在，只是壳。
package main

import (
	"os"

	"github.com/hueshu/relaymic/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], cli.Commands(), os.Stdout, os.Stderr))
}
