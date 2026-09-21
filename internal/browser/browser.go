// Package browser 用系统默认浏览器打开一个地址。
//
// 存在的理由只有一个：relaymic 敲下去就该看见页面，而不是先让人把地址抄进
// 浏览器。除开浏览器这一步，这个包什么都不做。
package browser

import (
	"net"
	"os/exec"
	"runtime"
)

// Command 返回在该系统上打开 url 的命令。
//
// 抽成纯函数是为了能测：真的开一次浏览器会弹窗，测试里只能验挑的是哪个命令。
func Command(goos, url string) (string, []string) {
	switch goos {
	case "windows":
		// rundll32 不经过 shell 关联，也不像 cmd 的 start 那样要先起一个 cmd。
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		return "open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// Open 打开 url，不等浏览器退出。
func Open(url string) error {
	name, args := Command(runtime.GOOS, url)
	return exec.Command(name, args...).Start()
}

// ConsoleURL 把监听地址变成浏览器里能打开的地址。
//
// 0.0.0.0 和 :: 是"所有网卡"，是监听用的写法，不是能访问的地址 —— 直接丢给
// 浏览器只会得到一句"无法访问"。绑在所有网卡上时，本机就用回环进去。
func ConsoleURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr + "/monitor"
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/monitor"
}
