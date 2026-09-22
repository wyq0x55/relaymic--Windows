# RelayMic

> **中文** · [English](README.md)

**把任意设备浏览器里的麦克风，变成一台远程 Mac 上的系统输入设备。**

远程桌面转发你的画面、键盘、鼠标——不转发你的声音。Windows 的 RDP 有麦克风重定向，
但对端是 Mac 时，市面上每一个远程工具都没有这个功能。不是藏在某个菜单里，是不存在。

RelayMic 补的就是这一段。

```
你面前的设备（任意浏览器）
  ↓  采集麦克风 → Opus 48kHz 立体声
  ↓  WebRTC 加密传输（优先直连，打不通走 TURN 中继）
远程的 Mac
  ↓  解码 → 抖动缓冲 → 写入 BlackHole 虚拟音频设备
Zoom / 听写 / Audacity / 任何应用 —— 当成普通麦克风读
```

它**不替代**你的远程桌面软件，和 TeamViewer、AnyDesk、Parsec、RustDesk、Jump Desktop
并行跑，那些工具继续管画面和输入，不知道也不需要知道 RelayMic 的存在。

## 远程连到 Mac 时，谁能传你的麦克风

| 工具 | 画面 / 键鼠 | 你的麦克风 |
|---|---|---|
| TeamViewer | ✅ | ❌ 用的是那台 Mac 自己的麦克风 |
| AnyDesk | ✅ | ❌ |
| Parsec | ✅ | ❌ |
| RustDesk | ✅ | ❌ |
| Jump Desktop | ✅ | ❌ |
| macOS 屏幕共享 | ✅ | ❌ |
| Microsoft RDP（且对端是 Windows） | ✅ | ✅ 系统自带，但止步于 Windows |
| **RelayMic** | 交给你的远程工具 | ✅ 作为系统输入设备 |

## 安装

**把这个仓库交给你的 AI 助手，让它读 [`SETUP.md`](SETUP.md)。**

Claude Code、Codex、Cursor 都行。它会装依赖、编译、跑起来，然后教你怎么用。你不用
自己敲命令。

```
克隆下来，然后对你的 AI 说：
「照着 SETUP.md 把 RelayMic 装好，装完教我怎么用。」
```

`SETUP.md` 是按 AI 能直接执行的方式写的：每步都有验证方法，失败了有排查表。

**想把它交给别人用**（一个 exe + 一个 token + 一个 Hub 地址）：见
[`docs/handoff.zh-CN.md`](docs/handoff.zh-CN.md)。

想自己动手也可以，那份文档人也读得懂。大致是：两端装 Tailscale 组网 →
Mac 上 `brew install opus && brew install --cask blackhole-2ch` →
`go build -tags nolibopusfile ./cmd/receiver` → 跑起来 → 在另一台设备的浏览器打开
`https://<mac 的 tailnet IP>:7420`。

## 命令行入口

只有一个入口：`relaymic`。

```bash
relaymic                       # 起本机应用：接收端 + 控制台页面，并打开浏览器
relaymic receiver -hub wss://… # 同一件事，把参数写全
relaymic help                  # 全部子命令
relaymic version
```

不带参数时它绑 `127.0.0.1:7420` 并用默认浏览器打开控制台：配对码、链路模式、电平、
最近日志都在那一页上，Hub 地址和音频设备也能在页面上改。不想要控制台就显式写
`-monitor ""`。

`relaymic receiver|sender|signaling|probe|selfcheck|turncheck|stuncheck` 对应原来的
`relaymic-receiver`、`relaymic-sender` 等二进制；那些老名字还在，现在只是一层壳。

## 现状

**这是作者自用工具的开源版本，不是打磨过的消费级产品。**

- 命令行启动，没有图形界面，没有安装包
- 界面和日志文案目前是中文
- **需要先组网**：当前版本没有公网信令服务器，发送端浏览器必须能直接访问
  Mac 的 `7420` 端口。实际方案是 **Tailscale**（免费，两端装完各自拿一个稳定的
  `100.x.x.x`，7420 直达、WebRTC 也在虚拟网内直连）。国内家宽普遍在运营商级 NAT
  后面，没有公网 IP，端口映射和 DDNS 都救不了——这条路走不通
- Mac 上的输入设备显示为 `BlackHole 2ch`，不叫 RelayMic

但**音频链路本身经过长期实战**——作者三台 Mac 日常在用。下面这些参数都是踩坑换来的，
不建议"优化"：

- **抖动缓冲 150ms**：实测值。局域网上 20ms 很爽，酒店 Wi-Fi 上立刻断续
- **静音抑制（DTX）默认关**：它省带宽，代价是削掉轻声说话的词头，听写会丢第一个音节
- **三路诊断录音**：处理前、缓冲后、从虚拟设备读回。"听着不对"能变成一段可以指着看
  的波形

延迟大致等于一通电话：网络往返 + 150ms 缓冲。适合说话、听写、开会；不适合录音时
监听自己的声音。

## 你可能不需要它

- **同一个房间里的 iPhone 想当 Mac 的麦克风** → Apple 的连续互通麦克风是免费的
- **Windows 连 Windows** → RDP 自带麦克风重定向，免费
- RelayMic 解决的是**距离**：不同建筑、不同城市、不同国家

## 隐私

音频走 WebRTC 加密的点对点连接。能直连时完全不经过任何第三方；打不通需要中继时，
中继只转发加密包。

**没有作者运营的服务器参与，也就无从记录。** TURN 是你自己配的。代码在这里，可以
自己核对——这也是开源的意义之一：处理你声音的东西，说"我不存储"不如让你自己看。

## 仓库结构

```
cmd/relaymic     唯一入口。`relaymic` 起本机应用（接收端 + 控制台页面）；
                 `relaymic <命令>` 跑下面某个命令
cmd/receiver     接收端：收流、解码、写入虚拟设备；控制台页面就是它的配置界面
cmd/sender       命令行发送端
cmd/probe,selfcheck,stuncheck,turncheck   诊断工具
internal/cli     子命令分发与用法
internal/app     各命令的实现；cmd/* 只是薄壳
internal/audio   音频设备与处理链
internal/rtc     WebRTC 收发
internal/sender  发送端引擎
internal/web     网页发送端（嵌入二进制）
internal/browser 用系统默认浏览器打开控制台
site/            relaymic.com 落地页（Cloudflare Workers）
docs/            设计与决策记录
```

## 不打算做的事

这些都认真考虑过并否决了，写下来是为了省掉重复讨论：

- **原生图形界面**。本机控制台页面就是界面，老的 `cmd/sender-gui` 直接删掉，不留冻结版
- **Windows → Windows**。微软的 RDP 自带麦克风重定向，免费且更好用，没有理由重做
- **同屋 iPhone → Mac 主打这个场景**。Apple 的连续互通麦克风免费，打不过也没必要打
- **改动音频链的实测参数**。缓冲 150ms、DTX 关闭、增益渐变门、拉伸回补——每一个都是
  某次故障之后调出来的。看着像可以优化的地方，多半是别人已经踩过的坑

## 构建

```bash
brew install opus
go build -tags nolibopusfile ./...
go test  -tags nolibopusfile ./internal/...
```

`-tags nolibopusfile` 是必须的——只用编解码，不读 `.opus` 文件，不加会去链接
libopusfile 然后失败。

要一个单文件 exe（静态链接 opus，不用带 DLL）：

```bash
go build -tags nolibopusfile -ldflags "-extldflags -static" -o relaymic.exe ./cmd/relaymic
```

## 许可

[AGPL-3.0](LICENSE)。

你可以运行、研究、修改、分发它，**包括拿去商用**。但如果你分发了修改过的版本，
**或者让别人通过网络使用你改过的版本**，你就欠这些人一份完整源码，且必须用同一个
许可证——把改过的版本跑在服务器上给人用也算，哪怕你从没把程序发给过谁。

如果你需要在这份代码上做闭源产品，版权在我手上，可以单独授权：<hey@relaymic.com>。

BlackHole 是独立的开源项目（MIT），RelayMic 引导你用 Homebrew 安装官方包，
不捆绑分发。
