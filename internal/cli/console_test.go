package cli

import "testing"

// 双击启动失败时要留住窗口：不然用户只看见闪一下，不知道为什么没起来。
func TestShouldPauseOnlyForADoubleClickFailure(t *testing.T) {
	cases := []struct {
		name           string
		args           []string
		code           int
		stdinIsConsole bool
		want           bool
	}{
		{name: "双击且失败", args: nil, code: 1, stdinIsConsole: true, want: true},
		{name: "双击但跑成功了", args: nil, code: 0, stdinIsConsole: true, want: false},
		{name: "命令行里失败", args: []string{"-hub", "wss://h"}, code: 1, stdinIsConsole: true, want: false},
		{name: "子命令失败", args: []string{"probe"}, code: 2, stdinIsConsole: true, want: false},
		{name: "输出被接走时不能等人", args: nil, code: 1, stdinIsConsole: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldPause(tc.args, tc.code, tc.stdinIsConsole); got != tc.want {
				t.Fatalf("ShouldPause(%v, %d, %v) = %v，want %v", tc.args, tc.code, tc.stdinIsConsole, got, tc.want)
			}
		})
	}
}

func TestIsConsoleRejectsNothing(t *testing.T) {
	if IsConsole(nil) {
		t.Fatal("IsConsole(nil) = true")
	}
}
