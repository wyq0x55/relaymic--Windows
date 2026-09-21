// relaymic-probe 是老名字下的同一个命令。
//
// 现在的入口是 relaymic（relaymic probe）。这个二进制留着，因为 CI、文档，
// 以及已经装在机器上的脚本里写的是这个名字。
package main

import (
	"os"

	"github.com/hueshu/relaymic/internal/app/probe"
)

func main() { os.Exit(probe.Main(os.Args[1:])) }
