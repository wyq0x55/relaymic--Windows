# RelayMic 安装指南（给 AI 助手执行）

> **中文** · [English](SETUP.md)

> **人类读者**：把这个文件连同仓库交给你的 AI 助手（Claude Code、Codex、Cursor
> 等），让它照着装。装完它会教你怎么用。你自己不用敲这里的任何命令。

---

## 给 AI 的说明

你正在帮用户安装 RelayMic。请**按顺序执行**，每一步都有验证方法，**验证不过就停下来
排查，不要继续往下走**。全部装完后，跳到最后一节「装完之后教用户什么」。

这是个早期项目，形态是命令行工具，不是打包好的 App。安装过程需要编译。
如果用户问"为什么这么麻烦"，如实说：作者把自用工具开源了，没做安装包。

**语言**：仓库里的日志和界面文案目前是中文。如果用户不懂中文，你需要在教他使用时
翻译界面上的字。

---

## 它是什么

把任意设备浏览器里的麦克风，变成一台远程 Mac 上的系统输入设备。

远程桌面软件（TeamViewer、AnyDesk、Parsec、RustDesk、Jump Desktop、macOS 屏幕共享）
都只转发画面、键盘、鼠标，**不转发你的麦克风**。Windows 的 RDP 有麦克风重定向，但
对端是 Mac 时这个功能不存在。RelayMic 补的就是这一段。

数据流：

```
你面前的设备（任意浏览器）
  ↓ 采集麦克风 → Opus 48kHz 立体声编码
  ↓ WebRTC 加密传输（能直连就直连，不能直连走 TURN 中继）
远程 Mac 上的 relaymic receiver
  ↓ 解码 → 抖动缓冲 → 写入 BlackHole 虚拟音频设备
Mac 上任何应用（Zoom / 听写 / Audacity …）把 BlackHole 当普通麦克风读
```

RelayMic **不替代**远程桌面软件，它和你已经在用的那个并行跑。

---

## 前置条件

| 项 | 要求 | 检查命令 |
|---|---|---|
| 远程 Mac | macOS 12+ | `sw_vers -productVersion` |
| Homebrew | 已安装 | `brew --version` |
| Go | 1.26+ | `go version` |
| 发送端 | 任意带麦克风的浏览器 | — |

### 网络可达性——**先解决这个，否则后面白装**

RelayMic 需要两条通路，缺一不可：

1. **信令**：发送端浏览器要能访问 `https://<mac>:7420`，拿网页和交换 SDP
2. **媒体**：WebRTC 音频流，优先直连，打不通走 TURN

第 1 条最容易被忽略。**它要求 Mac 的 7420 端口对发送端可达**——这在跨公网时本身
就是难题。当前版本没有公网信令服务器（那是未完成的产品化计划），所以必须靠组网
解决。

三种方案，按可行性排序：

| 方案 | 信令可达 | 媒体连通 | 国内家宽可行性 |
|---|---|---|---|
| **Tailscale**（推荐） | ✅ tailnet 内直达 | ✅ 已在虚拟网内直连 | ✅ 免费，装完就能用 |
| 公网 IP + 端口映射 + DDNS | ⚠️ 需固定或动态域名 | 需 STUN，可能需 TURN | ❌ 运营商级 NAT 下做不到 |
| 同一局域网 | ✅ | ✅ | ✅ 但这场景通常不需要 RelayMic |

**默认走 Tailscale。** 它是 WireGuard overlay，两端装完各自拿到一个稳定的 `100.x.x.x`
地址，7420 端口直接可达，WebRTC 也在这个虚拟网内直连——连 TURN 都基本用不上。这也
是作者自己的部署方式，`internal/discover` 的自动发现就是围绕它建的。

```bash
# 两台设备都要装
brew install --cask tailscale        # Mac
# 其他平台去 https://tailscale.com/download
```

装完让用户在两端用同一个账号登录，然后：

```bash
tailscale ip -4        # 拿到 Mac 的 tailnet IP，形如 100.x.x.x
```

**验证**：在发送端设备上 ping 这个地址，通了才继续。

> **同一局域网的用户注意**：如果两台设备真在同一个 LAN 里，直接用
> `192.168.x.x` 就行，不用装 Tailscale。但先想清楚——同屋的话，Mac 用
> Apple「连续互通麦克风」接 iPhone 是免费的，未必需要 RelayMic。

Go 和 Homebrew 缺失时的安装：

```bash
# Homebrew（如果没有）
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Go（如果没有）
brew install go
```

---

## 第 1 步：装音频依赖

**在远程 Mac 上执行**（就是你要把声音送过去的那台）。

```bash
brew install opus
brew install --cask blackhole-2ch
```

- `opus` 是音频编解码库，编译时通过 cgo 链接。
- `blackhole-2ch` 是开源的虚拟音频设备（MIT 许可），RelayMic 把解码后的声音写进
  它，Mac 上其他应用再从它读。**RelayMic 不捆绑分发它**，走 Homebrew 装官方包。

**BlackHole 安装会弹系统提示要求授权**（它是音频驱动，需要用户批准）。让用户在
`系统设置 → 隐私与安全性` 里点「允许」。装完可能需要重启，或者至少注销重登。

**验证**：

```bash
system_profiler SPAudioDataType | grep -i blackhole
```

必须能看到 `BlackHole 2ch`。看不到就是驱动没生效，让用户重启后再试。**这一步不过
不要继续**，后面全都依赖它。

---

## 第 2 步：编译

```bash
cd <仓库目录>
go build -tags nolibopusfile -o bin/relaymic-receiver ./cmd/receiver
```

`-tags nolibopusfile` 是必须的：不加会去找 libopusfile（我们只用编解码，不读 .opus
文件），在只装了 `opus` 的机器上会链接失败。

**验证**：

```bash
./bin/relaymic-receiver -h
```

能打印出参数列表就算成功。

编译报 `opus/opus.h: No such file` 之类的错，说明 cgo 找不到 Homebrew 的头文件。
Apple Silicon 上试：

```bash
export CGO_CFLAGS="-I$(brew --prefix)/include"
export CGO_LDFLAGS="-L$(brew --prefix)/lib"
```

然后重新编译。

---

## 第 3 步：首次运行

```bash
./bin/relaymic-receiver
```

正常启动会打印类似：

```
监听 :7420
发送端地址: https://localhost:7420
发送端地址: https://192.168.1.23:7420
```

**挑出发送端要用的那个地址**：装了 Tailscale 就用 `100.x.x.x` 那条，同局域网用
`192.168.x.x`。用户待会要在另一台设备上打开它。

几个正常现象，别当成故障：

- **启动时 Mac 会"说话"**（一声很短的语音）。这是刻意的：macOS 上 BlackHole 空置久了
  会回到未激活状态，非 Apple 进程首次打开会挂死，所以代码里先用系统 `say` 命令唤醒
  它一次。这是踩坑换来的，不要"优化"掉。
- **打开设备失败会在进程内重试**，不退出。同样是刻意的——退出重启会在 coreaudiod
  里留下杀不掉的残留，越试越打不开。

**验证**：另开一个终端

```bash
curl -sk https://localhost:7420/api/status
```

返回 JSON 就说明服务活着。**用 API 验证，不要用 `pgrep` 看进程在不在**——进程活着
不等于音频链路通。

---

## 第 4 步：连上发送端

在**另一台设备**（用户面前那台，Windows / iPad / 另一台 Mac 都行）的浏览器里打开
Mac 的地址加端口 `7420`：

- 走 Tailscale：`https://100.x.x.x:7420`（用 `tailscale ip -4` 拿到的那个）
- 同一局域网：`https://192.168.x.x:7420`

第 3 步启动时打印的「发送端地址」里会列出本机所有网卡的 IP，**装了 Tailscale 的话
`100.x.x.x` 那条就在里面**，挑它。

**浏览器会警告证书不安全**——这是正常的，RelayMic 用的是自签证书（局域网工具没法
签发受信任证书）。让用户点「高级 → 继续前往」。

- Chrome/Edge：`高级` → `继续前往 192.168.1.23（不安全）`
- Safari：`显示详细信息` → `访问此网站`

> **为什么必须 HTTPS**：浏览器只在安全上下文里给麦克风权限。`http://` 页面调不了
> `getUserMedia`。所以自签证书不是懒，是硬性要求。

页面打开后：

1. 浏览器弹出麦克风权限请求 → 让用户点「允许」
2. 点页面上的「开始说话」按钮
3. 状态从「未连接」变成已连接，电平条开始跳动

---

## 第 5 步：在 Mac 上收声音

回到远程 Mac。现在声音已经进了 BlackHole，任何应用把 BlackHole 当输入设备就能收到。

**告诉用户**：在需要用麦克风的应用里，把输入设备选成 **`BlackHole 2ch`**。

- Zoom：`设置 → 音频 → 麦克风` → 选 `BlackHole 2ch`
- 系统听写：`系统设置 → 声音 → 输入` → 选 `BlackHole 2ch`
- Audacity / OBS / 任何应用：在各自的音频输入设置里选它

> **注意**：设备列表里显示的名字是 **`BlackHole 2ch`**，不是 "RelayMic"。这是当前
> 版本的实际情况。

**验证整条链路**：

```bash
./bin/relaymic-receiver -meter
```

加 `-meter` 会每秒打印一次收到的电平。让用户对着发送端设备说话，终端里应该看到
电平条跳动。有跳动 = 音频真的到了 Mac。

或者打开监控页：`https://<mac-ip>:7420/monitor`，有波形和统计。

---

## 常用参数

```bash
./bin/relaymic-receiver \
  -addr :7420 \           # 监听地址
  -device blackhole \     # 输出设备名（子串匹配，不区分大小写）
  -buffer 150 \           # 抖动缓冲深度（毫秒）
  -meter                  # 打印电平，诊断用
```

**`-buffer 150` 不要随便调小。** 150ms 是在真实网络上测出来的，不是拍的。局域网上
20ms 听着很爽，换成酒店 Wi-Fi 就开始断续。同理，`-dtx` 默认关闭是因为静音抑制会
削掉轻声说话时的词头，听写会丢第一个音节。

**跨公网连接**需要 TURN 中继（两端 NAT 打不通时兜底）：

```bash
./bin/relaymic-receiver \
  -turn turn:your-turn-server:3478 \
  -turn-user <用户名> \
  -turn-pass <密码>
```

没有自己的 TURN 服务器就只能在能直连的网络里用（局域网、Tailscale、VPN）。
Cloudflare Realtime TURN 有免费额度，可以自己申请一个。

---

## 后台常驻（可选）

用户如果希望关掉终端后继续运行，用 launchd。写一个
`~/Library/LaunchAgents/com.relaymic.receiver.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.relaymic.receiver</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/<用户名>/relaymic/bin/relaymic-receiver</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/relaymic.log</string>
  <key>StandardErrorPath</key><string>/tmp/relaymic.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.relaymic.receiver.plist
```

**更新二进制时先 `rm` 再 `cp`，不要直接覆盖。** 直接覆盖正在运行的可执行文件，
macOS 会让新旧混在一起，表现是启动后行为诡异。

---

## 排查

| 症状 | 原因与处理 |
|---|---|
| 编译报找不到 `opus.h` | cgo 找不到 Homebrew 头文件，设 `CGO_CFLAGS` / `CGO_LDFLAGS`，见第 2 步 |
| 启动报「音频初始化失败」 | BlackHole 没装好或没授权。回第 1 步验证 `system_profiler` |
| 启动卡住不动 | BlackHole 处于未激活态。先手动 `say -a "BlackHole 2ch" " "` 唤醒再启动 |
| 浏览器没有麦克风权限弹窗 | 页面不是 HTTPS，或用户之前拒绝过。去浏览器站点设置里重置权限 |
| 页面根本打不开 | 7420 端口不可达。这是信令层问题，不是 WebRTC 问题——检查 Tailscale 两端是否都在线：`tailscale status` |
| 页面能开但连不上 | 信令通了媒体没通。Tailscale 内一般不会发生；跨公网需配 TURN |
| 连上了但 Mac 收不到声 | 应用里选错了输入设备。必须选 `BlackHole 2ch` |
| 声音断续 | 网络抖动。`-buffer` 调大到 200~300 试试 |
| 听写丢字头 | 不要开 `-dtx`（默认就是关的） |
| 反复启动后彻底打不开设备 | coreaudiod 里攒了残留：`sudo killall coreaudiod`（会短暂中断系统音频） |

诊断工具（`cmd/` 下）：

```bash
go run -tags nolibopusfile ./cmd/selfcheck    # 环境自检
go run -tags nolibopusfile ./cmd/stuncheck    # STUN 连通性
go run -tags nolibopusfile ./cmd/turncheck    # TURN 连通性
go run -tags nolibopusfile ./cmd/probe        # 音频设备探测
```

---

## 装完之后教用户什么

装完别只说"装好了"，把下面这些讲清楚（用用户的语言）：

1. **日常怎么启动**：跑哪个命令，或者已经配了 launchd 就说明它开机自启
2. **发送端地址是什么**：那个 `https://<ip>:7420`，让用户存成书签
3. **证书警告是正常的**：每次换设备第一次访问都要点一次「继续前往」
4. **在应用里选 `BlackHole 2ch`**：这是最容易卡住的一步，说清楚设备名不叫 RelayMic
5. **延迟大概是多少**：网络往返 + 150ms 缓冲，接近一通电话。适合说话、听写、开会，
   **不适合录音时监听自己的声音**
6. **隐私**：音频走 WebRTC 加密的点对点连接，能直连时完全不经过第三方；作者没有
   任何服务器参与，也就无从记录。代码是开源的，可以自己核对

如果用户的场景是「同一个房间里的 iPhone 当 Mac 麦克风」，告诉他 **不需要 RelayMic**
——Apple 的「连续互通麦克风」是免费的。RelayMic 解决的是距离问题：不同建筑、
不同城市、不同国家。

---

## 项目现状（如实告诉用户）

- 这是作者自用工具的开源版本，**不是打磨过的消费级产品**
- 命令行启动，没有图形界面，没有安装包
- 界面和日志文案目前是中文
- 落地页 relaymic.com 上描述的「六位配对码」「一键安装包」**尚未实现**，那是
  未完成的产品化计划
- 但**音频链路本身是经过长期实战的**：作者三台 Mac 日常在用，缓冲深度、静音抑制、
  增益策略这些参数都是踩坑调出来的

采用 AGPL-3.0 许可。有问题去 GitHub 提 issue。
