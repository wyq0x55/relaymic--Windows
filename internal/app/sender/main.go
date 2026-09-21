// sender 是原生发送端的命令行入口：把本机麦克风推给接收端。
//
// 它取代浏览器网页的理由是控制权。浏览器那条路上，降噪、自动增益、
// DTX 都攥在 Chrome 手里，出了问题只能靠 SDP 隔空遥控；这里编码器
// 在自己手里，每个开关都是明拨的。核心逻辑在 internal/sender，
// 与图形界面版（cmd/sender-gui）共用。
package sender

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/hueshu/relaymic/internal/sender"
)

// Main 以命令行方式运行 sender。args 是命令名之后的参数。
func Main(args []string) int {
	os.Args = append([]string{"relaymic sender"}, args...)
	run()
	return 0
}

func run() {
	target := flag.String("target", "", "接收端地址，多个用逗号分隔；留空则纯靠自动发现")
	discover := flag.Bool("discover", true, "自动发现 tailnet 内的接收端并加入广播")
	deviceName := flag.String("device", "", "输入设备名（子串匹配），留空用系统默认麦克风")
	bitrate := flag.Int("bitrate", 96000, "Opus 码率（bps）")
	listDevices := flag.Bool("list", false, "列出输入设备后退出")
	meter := flag.Bool("meter", false, "每秒打印一次采集电平")
	flag.Parse()

	log.SetFlags(log.Ltime)

	if *listDevices {
		names, err := sender.ListMics()
		if err != nil {
			die(err)
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}

	if *target == "" && !*discover {
		die(fmt.Errorf("要么指定 -target，要么开着 -discover"))
	}

	targets := []string{}
	for _, t := range strings.Split(*target, ",") {
		if t = strings.TrimSpace(t); t != "" {
			targets = append(targets, t)
		}
	}
	eng := sender.New(sender.Config{
		Targets:  targets,
		Discover: *discover,
		Device:   *deviceName,
		Bitrate:  *bitrate,
	})
	eng.OnState = func(target, s string) { log.Println(target, s) }
	if *meter {
		eng.OnLevel = func(db float64) { log.Printf("电平 %6.1f dBFS", db) }
	}

	if err := eng.Start(); err != nil {
		die(err)
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
	eng.Stop()
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
