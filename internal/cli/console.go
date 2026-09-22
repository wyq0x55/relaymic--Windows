package cli

import "os"

// ShouldPause 判断这次退出要不要"等一个回车"。
//
// 双击启动时没有参数、进程连着控制台：出错后窗口瞬间关掉，用户只看见闪一下，
// 看不到原因。带参数跑（命令行里）不该多这一步 —— 输出本来就留在终端里。
func ShouldPause(args []string, code int, stdinIsConsole bool) bool {
	return code != 0 && len(args) == 0 && stdinIsConsole
}

// IsConsole 判断这个文件是不是连着控制台（不是管道、不是重定向）。
//
// 双击启动时 stdin 是控制台；被别的东西接走时不是 —— 那种情况下等人按回车
// 就是把自己挂住。
func IsConsole(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
