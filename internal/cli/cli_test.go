package cli

import (
	"bytes"
	"strings"
	"testing"
)

// 测试用的命令表：Run 只负责分发，不负责具体命令做什么。
func testCommands(ran *[]string) []Command {
	return []Command{
		{Name: "receiver", Summary: "接收端", Run: func(args []string) int { *ran = append(*ran, "receiver "+strings.Join(args, " ")); return 0 }},
		{Name: "probe", Summary: "设备检查", Run: func(args []string) int { *ran = append(*ran, "probe"); return 0 }},
	}
}

func TestRunWithoutArgumentsStartsTheApp(t *testing.T) {
	var ran []string
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

	code := Run(nil, testCommands(&ran), out, errOut)

	if code != 0 {
		t.Fatalf("退出码 = %d，want 0（stderr: %s）", code, errOut)
	}
	// 无参数 = 起本机应用：接收端 + 控制台。控制台地址得自动补上，
	// 否则用户敲完 relaymic 什么页面都看不到。
	if len(ran) != 1 || !strings.HasPrefix(ran[0], "receiver ") {
		t.Fatalf("跑了 %v，want 只有 receiver", ran)
	}
	if !strings.Contains(ran[0], "-monitor "+DefaultMonitorAddr) {
		t.Fatalf("没有自动补上控制台地址：%q", ran[0])
	}
	// 光有地址不够：敲 relaymic 的人该直接看见页面。
	if !strings.Contains(ran[0], "-open") {
		t.Fatalf("没有自动打开页面：%q", ran[0])
	}
}

func TestRunDispatchesToTheNamedCommand(t *testing.T) {
	var ran []string
	code := Run([]string{"probe"}, testCommands(&ran), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 || len(ran) != 1 || ran[0] != "probe" {
		t.Fatalf("code=%d ran=%v", code, ran)
	}
}

// 显式写了子命令就是"这次怎么跑"，不该再被塞默认参数。
func TestRunKeepsExplicitArgumentsUntouched(t *testing.T) {
	var ran []string
	code := Run([]string{"receiver", "-hub", "wss://example.invalid/ws/receiver"}, testCommands(&ran), &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 || len(ran) != 1 {
		t.Fatalf("code=%d ran=%v", code, ran)
	}
	if strings.Contains(ran[0], DefaultMonitorAddr) {
		t.Fatalf("显式子命令不该被注入默认控制台地址：%q", ran[0])
	}
}

func TestRunPrintsUsageWithoutRunningAnything(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		var ran []string
		out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
		code := Run(args, testCommands(&ran), out, errOut)
		if code != 0 {
			t.Fatalf("%v 退出码 = %d，want 0", args, code)
		}
		if len(ran) != 0 {
			t.Fatalf("%v 竟然执行了命令：%v", args, ran)
		}
		usage := out.String()
		for _, want := range []string{"receiver", "probe", "用法"} {
			if !strings.Contains(usage, want) {
				t.Fatalf("%v 的用法里缺 %q：%s", args, want, usage)
			}
		}
	}
}

func TestRunPrintsTheVersion(t *testing.T) {
	var ran []string
	out := &bytes.Buffer{}
	code := Run([]string{"version"}, testCommands(&ran), out, &bytes.Buffer{})
	if code != 0 || len(ran) != 0 {
		t.Fatalf("code=%d ran=%v", code, ran)
	}
	if !strings.Contains(out.String(), Version) {
		t.Fatalf("版本输出里没有 %q：%s", Version, out)
	}
}

// 打错命令名时必须给一条出路，而不是静默地去跑默认应用。
func TestRunRejectsAnUnknownCommand(t *testing.T) {
	var ran []string
	errOut := &bytes.Buffer{}
	code := Run([]string{"recevier"}, testCommands(&ran), &bytes.Buffer{}, errOut)
	if code == 0 {
		t.Fatal("未知子命令不该返回 0")
	}
	if len(ran) != 0 {
		t.Fatalf("未知子命令竟然执行了：%v", ran)
	}
	if !strings.Contains(errOut.String(), "recevier") || !strings.Contains(errOut.String(), "receiver") {
		t.Fatalf("错误信息没把拼错的名字和可选命令都说出来：%s", errOut)
	}
}

func TestAppArgsKeepsWhatTheUserWrote(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "空参数补默认", in: nil, want: []string{"-monitor", DefaultMonitorAddr, "-open"}},
		{name: "只有别的参数", in: []string{"-hub", "wss://h"}, want: []string{"-hub", "wss://h", "-monitor", DefaultMonitorAddr, "-open"}},
		{name: "显式给了地址", in: []string{"-monitor", "127.0.0.1:9000"}, want: []string{"-monitor", "127.0.0.1:9000", "-open"}},
		{name: "等号写法也算给了", in: []string{"--monitor=127.0.0.1:9000"}, want: []string{"--monitor=127.0.0.1:9000", "-open"}},
		{name: "显式关掉控制台", in: []string{"-monitor", ""}, want: []string{"-monitor", ""}},
		{name: "显式关掉浏览器", in: []string{"-open=false"}, want: []string{"-open=false", "-monitor", DefaultMonitorAddr}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appArgs(tc.in)
			if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
				t.Fatalf("appArgs(%v) = %v，want %v", tc.in, got, tc.want)
			}
		})
	}
}
