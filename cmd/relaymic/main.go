// relaymic 是 RelayMic 的唯一入口。
//
// 敲 relaymic 就是起本机应用：接收端加上本机控制台页面。要别的能力再加子命令，
// 用法见 relaymic help。旧名字（relaymic-receiver 等）都还在，只是壳。
package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/hueshu/relaymic/internal/cli"
)

func main() {
	args := os.Args[1:]
	code := cli.Run(args, cli.Commands(), os.Stdout, os.Stderr)
	// 双击启动失败时把窗口留住：不然只看见闪一下，不知道为什么没起来。
	if cli.ShouldPause(args, code, cli.IsConsole(os.Stdin)) {
		fmt.Fprintln(os.Stderr, "\n按回车退出…")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	os.Exit(code)
}
