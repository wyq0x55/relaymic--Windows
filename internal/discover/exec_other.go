//go:build !windows

package discover

import "os/exec"

// hideWindow 在非 Windows 平台无事可做。
func hideWindow(cmd *exec.Cmd) {}
