# 把 RelayMic 交给别人用

给的是**一个 exe、一个 token、一个 Hub 地址**。对方需要一台 Windows 电脑和一个
浏览器，不需要装 Go、不需要改网络设置、机器上也不开任何入站端口。

下文把 `<Hub 主机>` 换成你自己的域名或 IP（本文档不进服务器地址）。

## 一、你要准备的四样东西

| 给什么 | 从哪来 | 说明 |
| --- | --- | --- |
| `relaymic.exe` | GitHub Actions 的 `relaymic-windows` 产物，或本机 `go build -tags nolibopusfile -ldflags "-extldflags -static" -o relaymic.exe ./cmd/relaymic` | 约 28 MB，静态链接，对方不用装运行库 |
| 一个 token | `relaymic signaling -gen-receiver 对方的名字` | 一人一个，输出是 JSON |
| Hub 地址 | 你部署的控制面，形如 `wss://<Hub 主机>:9443/ws/receiver` | |
| 发送端页面 | `https://<Hub 主机>:9443/` | 对方用手机或另一台电脑打开 |

**推荐：在 Hub 的管理面上生成**（见 `docs/signaling.md` 的"管理面"一节）：
浏览器打开 `https://<Hub 主机>:9443/admin`，登录后填个名字点「生成 token」，
把显示出来的那一行发给对方。对方写进自己的 `receiver-token.txt` 就能用，
**不用重启 Hub，也不会打断正在通话的人**。

没有开管理面时，也可以在 VPS 上生成：

```bash
relaymic signaling -gen-receiver "别人的电脑"
```

```json
[
  { "name": "别人的电脑", "token": "qH4HX0AlT1eS96g3oM-pDgipG_a8bDKaFAb16PXEfqE" }
]
```

把这一条加进 Hub 的 `receivers` 再重启 Hub（这一步会掐断当前所有连接）：

```json
{
  "receivers": [
    { "name": "我的电脑", "token": "……" },
    { "name": "别人的电脑", "token": "……" }
  ]
}
```

重启会断开当前所有连接，正在通话的那一次要重新配对。加人之前先说一声 ——
这也是为什么值得把管理面开起来。

## 二、对方要做的

### 1. 装虚拟声卡

RelayMic 不装驱动，它写进系统里已有的虚拟声卡。装完**必须重启**。

- 麦克风方向（必需）：RelayMic → `CABLE Input`（播放）→ `CABLE Output`（录制）→ 会议软件当麦克风
- 回传方向（想让对方听见会议里的声音才需要）：会议软件扬声器 → `VoiceMeeter Aux Input`（播放）
  → `VoiceMeeter Aux Output`（录制）→ RelayMic 采走

最少一条线：VB-CABLE。要双向再加 Voicemeeter AUX VAIO（免费）。

### 2. 把整个文件夹拷过去

```
C:\RelayMic\relaymic.exe
C:\RelayMic\config.json             # 设置：Hub 地址、设备名
C:\RelayMic\receiver-token.txt      # 凭据：内容就一行 token
```

exe 旁边的 `config.json` 和 `receiver-token.txt` 会被自动采用（命令行参数仍然优先），
所以文件夹拷到哪台机器、哪个盘符都不用改路径。

### 3. 运行

```
C:\RelayMic\relaymic.exe
```

不带参数时它会：起接收端、在 `127.0.0.1:7420` 开控制台、并用默认浏览器打开它。
Windows 可能弹 SmartScreen（这个 exe 没有代码签名）：**更多信息 → 仍要运行**。

### 4. 第一次在页面上核对（通常不用改）

- 这些值文件夹里已经配好了：Hub 地址、输出设备、回传设备。
- 只需确认输出设备是 `CABLE Input (VB-Audio Virtual Cable)`（标着"虚拟线"）。
- 点「保存并重启接收端」

设备列表是现扫的，插好虚拟声卡后点「重新扫描设备」就能刷新。

### 5. 会议软件里选设备

- 麦克风 → `CABLE Output (VB-Audio Virtual Cable)`
- 扬声器 → `VoiceMeeter Aux Input (…)`（只有开了回传才需要）

### 6. 每次通话

1. 控制台页面上有一张 6 位配对码，5 分钟有效，过期会自动换一张。
2. 把码念给对方。
3. 对方在手机或另一台电脑打开 `https://<Hub 主机>:9443/`，输码，允许麦克风。

## 三、日常与更新

- 每次开机跑一次 `relaymic.exe`；想常驻就放启动项。
- 页面上的设置存在 `%USERPROFILE%\.config\relaymic\config.json`，下次启动自动读；
  命令行参数优先于文件里的值。换新版本 exe 时这份配置不用动。
- 对方在只放行 HTTP 代理的公司网络里时，加 `-turn-tunnel`（TURN 流量套进控制面隧道）。

## 四、常见问题

| 现象 | 原因与处理 |
| --- | --- |
| 提示"输出设备不唯一" | 机器上有不止一条虚拟线（`CABLE In 16ch` 和 `CABLE Input`）。在页面下拉里选全名 |
| "设备或资源忙" | 那条线被别的程序占着。关掉它，或换一条线 |
| 对方听不见 | 会议软件的麦克风没选 `CABLE Output`，或输出设备选成了物理设备 |
| 自己听不见会议里的声音 | 没开回传，或会议软件扬声器没选到第二条虚拟线 |
| 页面显示"等待 Hub 下发" | 没连上 Hub：核对地址、token、网络 |
| 一直卡在"连接控制面" | 网络挡了 WebSocket。先试 `-turn-tunnel`，再确认 `<Hub 主机>:9443` 可达 |
| 对方打不开发送端页面 | 那是你的 Hub，确认它在跑、证书没过期 |

## 五、安全与边界

- **token 是凭据，一人一个**。别发在群里；要收回某个人的访问，就把那一条从 Hub 配置里删掉并重启。
- Hub 只做配对和转发 SDP，音频不经过它；两端直连，打不通才走你自己配的 TURN。
- 证书：Hub 用的是公网 CA 签发的证书，对方的机器不需要装任何根证书就能连。
  这类证书有效期很短，**要确认自动续期在跑**，否则到期当天所有接收端都连不上。
- 这不是"隐身"工具：它不会隐藏会议里是一台设备还是两台，也不打算绕开公司 IT 或终端审计。
  对方要清楚这一点再用。

## 六、相关文档

- 部署 Hub 与 TURN：`docs/vps-signaling-turn-agent-guide.zh-CN.md`、`docs/signaling.md`
- 接收端细节（设备选择、回传、控制台）：`docs/windows-receiver.md`
- 控制台页面上能看什么：`docs/local-console.tdd.md`
