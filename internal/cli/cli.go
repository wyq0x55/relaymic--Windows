// Package cli 把 RelayMic 的各个命令收进一个入口。
//
// 用户只需要记一个名字：relaymic。敲 relaymic 就是起本机应用 —— 接收端加上
// 本机控制台页面；要别的能力再加子命令。命令表在 commands.go 里，这里只做
// 分发：分发能单独测，不必真去开音频设备。
package cli

import (
	"fmt"
	"io"
	"strings"
)

// DefaultMonitorAddr 是 relaymic 不带参数时控制台绑的地址。
//
// 只绑回环：控制台能看配对码、也能改设置，没有理由让局域网里的别人够得着。
const DefaultMonitorAddr = "127.0.0.1:7420"

// AppName 是无参数时跑的那个命令。
const AppName = "receiver"

// Version 是版本号，正式构建时用 -ldflags 覆盖。
var Version = "dev"

// Command 是一个子命令。
type Command struct {
	Name    string
	Summary string
	Run     func(args []string) int
}

// Run 解析命令行并执行，返回进程退出码。
func Run(args []string, cmds []Command, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runApp(cmds, nil, stderr)
	}

	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout, cmds)
		return 0
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "relaymic %s\n", Version)
		return 0
	}

	// relaymic -hub wss://… 是"起应用"的自然写法，和敲 relaymic 一样对待。
	if strings.HasPrefix(args[0], "-") {
		return runApp(cmds, args, stderr)
	}

	cmd, ok := find(cmds, args[0])
	if !ok {
		// 打错命令名时给一条出路，而不是静默地去跑默认应用 —— 那会让人以为
		// 参数生效了，其实跑的是另一件事。
		fmt.Fprintf(stderr, "relaymic: 没有 %q 这个命令\n\n", args[0])
		usage(stderr, cmds)
		return 2
	}
	// 接收端就是这个"敲下去该看见页面"的应用：显式写 receiver 也一样给它补上
	// 控制台默认值。不想开就显式写 -monitor "" 或 -open=false。
	if cmd.Name == AppName {
		return cmd.Run(appArgs(args[1:]))
	}
	return cmd.Run(args[1:])
}

// runApp 起本机应用：补上控制台地址后交给接收端。
func runApp(cmds []Command, args []string, stderr io.Writer) int {
	app, ok := find(cmds, AppName)
	if !ok {
		fmt.Fprintf(stderr, "relaymic: 内部错误：命令表里没有 %q\n", AppName)
		return 1
	}
	return app.Run(appArgs(args))
}

// appArgs 给"起应用"补上默认参数：控制台监听地址，以及用浏览器把它打开。
//
// 用户显式写过的就不动 —— 包括 -monitor ""（那是在说"别开控制台"，那也就
// 没有页面可开）。
func appArgs(args []string) []string {
	out := append([]string{}, args...)
	addr, given := monitorValue(out)
	if !given {
		addr = DefaultMonitorAddr
		out = append(out, "-monitor", addr)
	}
	if addr != "" && !hasFlag(out, "-open") {
		out = append(out, "-open")
	}
	return out
}

// monitorValue 取出命令行里显式给出的控制台地址，以及是否给过。
func monitorValue(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "-monitor" || arg == "--monitor":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		case strings.HasPrefix(arg, "-monitor="):
			return strings.TrimPrefix(arg, "-monitor="), true
		case strings.HasPrefix(arg, "--monitor="):
			return strings.TrimPrefix(arg, "--monitor="), true
		}
	}
	return "", false
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func find(cmds []Command, name string) (Command, bool) {
	for _, cmd := range cmds {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return Command{}, false
}

func usage(w io.Writer, cmds []Command) {
	fmt.Fprintln(w, "用法:")
	fmt.Fprintln(w, "  relaymic                    起本机应用：接收端 + 控制台页面")
	fmt.Fprintln(w, "  relaymic <命令> [参数...]    跑某个命令")
	fmt.Fprintln(w, "  relaymic help               这份用法")
	fmt.Fprintln(w, "  relaymic version            打印版本")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "命令:")
	width := 0
	for _, cmd := range cmds {
		if len(cmd.Name) > width {
			width = len(cmd.Name)
		}
	}
	for _, cmd := range cmds {
		fmt.Fprintf(w, "  %-*s  %s\n", width, cmd.Name, cmd.Summary)
	}
}
