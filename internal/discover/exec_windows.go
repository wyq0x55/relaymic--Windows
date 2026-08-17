//go:build windows

package discover

import (
	"os/exec"
	"syscall"
)

// hideWindow 阻止子进程弹出控制台窗口。
// GUI 程序（-H windowsgui）里 exec 命令行工具，Windows 默认会给子进程
// 开一个可见的 cmd 窗 —— 自动发现每 30 秒跑一次 tailscale，
// 用户看到的就是屏幕每半分钟闪一个黑框。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
