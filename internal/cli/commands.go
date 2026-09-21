package cli

import (
	"github.com/hueshu/relaymic/internal/app/probe"
	"github.com/hueshu/relaymic/internal/app/receiver"
	"github.com/hueshu/relaymic/internal/app/selfcheck"
	"github.com/hueshu/relaymic/internal/app/sender"
	"github.com/hueshu/relaymic/internal/app/signaling"
	"github.com/hueshu/relaymic/internal/app/stuncheck"
	"github.com/hueshu/relaymic/internal/app/turncheck"
)

// Commands 是 relaymic 能跑的全部命令。
//
// 顺序就是 help 里的顺序：最常用的排前面。发送端的图形界面（cmd/sender-gui）
// 不在这里 —— 它拖进整个 GUI 工具箱，不该让命令行程序跟着变重。
func Commands() []Command {
	return []Command{
		{Name: "receiver", Summary: "接收端 + 本机控制台（不带参数时默认跑这个）", Run: receiver.Main},
		{Name: "sender", Summary: "原生发送端：把本机麦克风推给接收端", Run: sender.Main},
		{Name: "signaling", Summary: "公网控制面：配对、转发 SDP、签发 TURN 凭据", Run: signaling.Main},
		{Name: "probe", Summary: "列出音频设备，或用测试音验证虚拟线", Run: probe.Main},
		{Name: "selfcheck", Summary: "冒充发送端，把测试音推给接收端", Run: selfcheck.Main},
		{Name: "turncheck", Summary: "验证 TURN 中继地址真的能收发", Run: turncheck.Main},
		{Name: "stuncheck", Summary: "判断本机 NAT 类型，决定要不要 TURN", Run: stuncheck.Main},
	}
}
