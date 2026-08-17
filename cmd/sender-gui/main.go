// sender-gui 是原生发送端的图形界面：双击打开、选麦克风、点"开始"。
//
// 核心逻辑全在 internal/sender，这里只做三件事：
// 把状态画出来、把选择存下来、把中文显示出来。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/hueshu/remotemic/internal/sender"
)

// config 是要记住的用户选择，存在用户配置目录下。
type config struct {
	Target string `json:"target"`
	Device string `json:"device"`
}

// shortHost 把 https://100.x.y.z:7420 缩成 100.x.y.z，状态行短一点。
func shortHost(target string) string {
	t := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://")
	if i := strings.Index(t, ":"); i > 0 {
		t = t[:i]
	}
	return t
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "remotemic", "sender.json")
}

func loadConfig() config {
	var c config
	p := configPath()
	if p == "" {
		return c
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	return c
}

func saveConfig(c config) {
	p := configPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	data, _ := json.Marshal(c)
	_ = os.WriteFile(p, data, 0o644)
}

// useSystemCJKFont 让 Fyne 用系统自带的中文字体。
// Fyne 内置字体不含中文，不设置的话界面全是豆腐块。
//
// 只能用单 .ttf：FYNE_FONT 对 .ttc 字体集合直接报
// "collections not allowed"，然后在渲染时空指针崩溃。
func useSystemCJKFont() {
	candidates := []string{
		`C:\Windows\Fonts\simhei.ttf`,                          // Windows 黑体，各版本都有
		`C:\Windows\Fonts\Deng.ttf`,                            // 等线，Win10+
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf", // macOS（开发测试用）
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			os.Setenv("FYNE_FONT", p)
			return
		}
	}
}

func main() {
	useSystemCJKFont()

	a := app.NewWithID("net.remotemic.sender")
	w := a.NewWindow("远程麦克风")
	w.Resize(fyne.NewSize(380, 300))

	cfg := loadConfig()

	// 默认自动发现 tailnet 里的所有接收端，界面上不需要填任何地址。
	// 手动框只做补充（不在 tailnet 里的机器才需要）。
	targetEntry := widget.NewMultiLineEntry()
	targetEntry.SetMinRowsVisible(2)
	targetEntry.SetPlaceHolder("自动发现已开启，此处留空即可\n（可补充 tailnet 外的地址，每行一个）")
	targetEntry.SetText(cfg.Target)

	mics, _ := sender.ListMics()
	micSelect := widget.NewSelect(mics, nil)
	if cfg.Device != "" {
		micSelect.SetSelected(cfg.Device)
	} else if len(mics) > 0 {
		micSelect.SetSelectedIndex(0)
	}

	status := widget.NewLabel("未连接（点开始后自动发现接收端）")
	levelBar := widget.NewProgressBar()
	levelBar.TextFormatter = func() string { return "" }

	var eng *sender.Engine
	var toggle *widget.Button
	toggle = widget.NewButton("开始说话", func() {
		if eng != nil {
			eng.Stop()
			eng = nil
			toggle.SetText("开始说话")
			levelBar.SetValue(0)
			return
		}

		cfg.Target = targetEntry.Text
		cfg.Device = micSelect.Selected
		saveConfig(cfg)

		targets := []string{}
		for _, t := range strings.Split(cfg.Target, "\n") {
			if t = strings.TrimSpace(t); t != "" {
				targets = append(targets, t)
			}
		}
		e := sender.New(sender.Config{Targets: targets, Discover: true, Device: cfg.Device})
		states := map[string]string{}
		order := []string{}
		e.OnState = func(target, s string) {
			fyne.Do(func() {
				if _, seen := states[target]; !seen {
					order = append(order, target) // 自动发现的目标按出现顺序排
				}
				states[target] = s
				lines := make([]string, 0, len(order))
				for _, t := range order {
					lines = append(lines, shortHost(t)+"  "+states[t])
				}
				status.SetText(strings.Join(lines, "\n"))
			})
		}
		e.OnLevel = func(db float64) {
			// -60dBFS 以下视为无声，0dBFS 满格
			v := (db + 60) / 60
			if v < 0 {
				v = 0
			}
			if v > 1 {
				v = 1
			}
			fyne.Do(func() { levelBar.SetValue(v) })
		}
		if err := e.Start(); err != nil {
			status.SetText("错误：" + err.Error())
			return
		}
		eng = e
		toggle.SetText("停止")
	})
	toggle.Importance = widget.HighImportance

	w.SetContent(container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("接收端", targetEntry),
			widget.NewFormItem("麦克风", micSelect),
		),
		toggle,
		levelBar,
		status,
	))

	w.SetCloseIntercept(func() {
		if eng != nil {
			eng.Stop()
		}
		a.Quit()
	})
	w.ShowAndRun()
}
