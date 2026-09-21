package browser

import "testing"

// 真的去开浏览器会弹出窗口，测试里只能验"挑的是哪个命令"。
func TestCommandPicksTheSystemOpener(t *testing.T) {
	const url = "http://127.0.0.1:7420/monitor"
	cases := []struct{ goos, want string }{
		{goos: "windows", want: "rundll32"},
		{goos: "darwin", want: "open"},
		{goos: "linux", want: "xdg-open"},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			name, args := Command(tc.goos, url)
			if name != tc.want {
				t.Fatalf("Command(%q) 用了 %q，want %q", tc.goos, name, tc.want)
			}
			if len(args) == 0 || args[len(args)-1] != url {
				t.Fatalf("Command(%q) 参数 = %v，最后一项应该是地址", tc.goos, args)
			}
		})
	}
}

// 0.0.0.0 和 :: 是"所有网卡"，不是浏览器能打开的地址。
func TestConsoleURLTurnsWildcardsIntoLoopback(t *testing.T) {
	cases := []struct{ addr, want string }{
		{addr: "127.0.0.1:7420", want: "http://127.0.0.1:7420/monitor"},
		{addr: "0.0.0.0:7420", want: "http://127.0.0.1:7420/monitor"},
		{addr: "[::]:7420", want: "http://127.0.0.1:7420/monitor"},
		{addr: ":7420", want: "http://127.0.0.1:7420/monitor"},
		{addr: "localhost:7420", want: "http://localhost:7420/monitor"},
		{addr: "192.168.1.5:7420", want: "http://192.168.1.5:7420/monitor"},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := ConsoleURL(tc.addr); got != tc.want {
				t.Fatalf("ConsoleURL(%q) = %q，want %q", tc.addr, got, tc.want)
			}
		})
	}
}
