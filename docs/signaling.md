# 公网 Signaling（#2）

RelayMic 原来的模型是"发送端必须能直接访问接收端的 `:7420`"。接收端在公司电脑上、
在 NAT 和公司防火墙后面，这条路走不通；上游的答案是让两台机器都进同一个 Tailscale
网络。这一版把那个前提换掉。

## 拓扑

```
你的电脑 / 手机                公网服务器                    公司电脑
   浏览器  ──HTTPS/WSS 443──►  relaymic-signaling  ◄──WSS 443──  接收端
      │                              │
      │  只交换 SDP，音频不经过这里    │
      └──────────── WebRTC 音频 ─────┴───────────────────────────►
                     P2P 优先，打不通时走 TURN（#3）
```

- **公网入口只有一个**：控制面的 443。
- **接收端不监听任何端口**。它主动拨出去，和浏览器打开一个 HTTPS 网站没有区别，
  因此公司防火墙不需要为它开任何入站规则。
- **不需要 Tailscale**。控制面是唯一的会合点，`internal/discover` 里的 Tailscale
  发现已经不在这条链路上。
- **音频不经过服务器**。控制面只搬 SDP；两端直连，打不通时在 #3 接 TURN。

## 使用流程

1. 接收端启动，主动连上控制面：

   ```
   relaymic-receiver.exe -hub wss://mic.example.com/ws/receiver -token-file C:\relaymic\token
   ```

   日志会打印当前配对码：

   ```
   已连接 wss://mic.example.com/ws/receiver
   设备: CABLE Input (VB-Audio Virtual Cable)
   配对码: 583921（5 分钟内有效，用过即换）
   ```

2. 你在任何设备的浏览器打开 `https://mic.example.com`，输入 `583921`，
   授权麦克风，点「开始说话」。

3. 音频进入公司电脑的 VB-CABLE，Teams 从 `CABLE Output` 读。

## 协议

控制面只有一条 WebSocket 通道和九种消息，没有"转发到任意目标"这种口子。

| 方向 | 类型 | 说明 |
|---|---|---|
| Hub → 接收端 | `waiting` | 新配对码 + 剩余秒数 |
| Hub → 接收端 | `joined` | 发送端已用码接入，带上会话标识 |
| Hub → 接收端 | `left` | 发送端离开，随后会发新码 |
| Hub → 发送端 | `paired` | 配对成功，带会话标识和接收端名字 |
| Hub → 发送端 | `closed` | 接收端离线，会话不可继续 |
| Hub → 任一端 | `error` | 可展示的错误（配对失败等） |
| 发送端 → Hub | `pair` | 用配对码认领接收端 |
| 发送端 → Hub → 接收端 | `offer` | 完整 SDP offer，原样搬运 |
| 接收端 → Hub → 发送端 | `answer` | 完整 SDP answer，原样搬运 |

SDP 是 `json.RawMessage`：控制面不解析、不改写、不缓存，因此不需要跟着 WebRTC
版本升级改代码。单条消息上限 64 KiB，超出直接断开。

`GET /api/ice` 用 HTTPS 下发 ICE 配置给浏览器（`#3` 接上 TURN 后填在这里）。

## 安全模型

配对码只有 6 位，所以它必须被当成"短期配对能力"，而不是身份认证。

| 面 | 做法 |
|---|---|
| 接收端身份 | 独立 256-bit Bearer token。Hub 只保存 SHA-256 摘要，不保存明文 |
| 配对码 | `crypto/rand` 均匀生成，5 分钟过期，成功即消费，一台接收端同时只有一个活码 |
| 防穷举 | 每客户端每分钟 5 次失败；单条连接 3 次失败即断开 |
| 失败响应 | 未知码、过期码、已用码、被限流返回**同一句话**，不给枚举预言机 |
| 浏览器来源 | 校验 `Origin`，默认只允许同源，可用 `allowedOrigins` 收窄 |
| 转发面 | 只允许 sender→offer、receiver→answer；其余消息类型一律断连 |
| 日志 | 不记录 token、配对码、原始 SDP、ICE 候选或对端 IP |

接收端重连时旧连接会被立即顶掉（`CloseNow`），避免两个会话同时挂同一台机器；
旧连接断开不会作废新连接的配对码。

## 部署

### 1. 生成接收端凭据

```bash
relaymic-signaling -gen-receiver 公司电脑
```

输出一段可直接粘进配置的 JSON。token 只在这里出现一次，请存到接收端的
`-token-file` 里（文件权限就是凭据的权限边界；命令行参数会出现在进程列表里）。

### 2. 配置文件

```json
{
  "listen": ":443",
  "certFile": "/etc/letsencrypt/live/mic.example.com/fullchain.pem",
  "keyFile": "/etc/letsencrypt/live/mic.example.com/privkey.pem",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [{"urls": ["stun:stun.l.google.com:19302"]}],
  "receivers": [{"name": "公司电脑", "token": "<32 字节 raw url-safe base64>"}]
}
```

配置里拼错的键会直接启动失败（`DisallowUnknownFields`）：把 `receivers` 写成
`receiver` 却静默忽略，等于上线后才发现一台机器都连不上。

### 3. 启动

```bash
relaymic-signaling -config /etc/relaymic/signaling.json
```

systemd 单元示例：

```ini
[Unit]
Description=RelayMic signaling
After=network-online.target

[Service]
ExecStart=/usr/local/bin/relaymic-signaling -config /etc/relaymic/signaling.json
Restart=always
RestartSec=2
User=relaymic

[Install]
WantedBy=multi-user.target
```

控制面是无状态的：配对码和会话都在内存里，重启即全部作废，接收端会自动重连并拿新码。

### 4. 反向代理（可选）

如果前面放 Nginx/Caddy，请让代理保留 `Origin` 和 `Authorization` 头，并把
`Upgrade`/`Connection` 透传给 `/ws/`。**限流要由代理负责**：`clientKey` 用的是
直连地址，不会把 `X-Forwarded-For` 当成可信输入。

## 接收端

| 参数 | 说明 |
|---|---|
| `-hub` | 控制面地址，形如 `wss://mic.example.com/ws/receiver`；必填 |
| `-token` / `-token-file` | 接收端凭据，二选一 |
| `-monitor` | 本机诊断页监听地址，例如 `127.0.0.1:7420`；**留空表示不监听任何端口** |
| `-device` | 虚拟音频设备；Windows 默认 `CABLE Input` |

接收端没有任何入站监听。要本机看波形就显式开 `-monitor`，并自己决定绑哪个地址。

## 本机自测

```powershell
relaymic-signaling -config signaling.json -addr 127.0.0.1:8099 -plain
relaymic-receiver -hub ws://127.0.0.1:8099/ws/receiver -token-file token.txt -device "CABLE Input"
```

`-plain` 只用于本机：浏览器只在安全上下文里给麦克风权限，所以真正的发送端页面必须走
HTTPS。`-self-signed-dir <目录>` 可以在没有证书的情况下用自签证书把 TLS 链路跑通。

## 这一版不做的事

- **TURN 还没有接**（#3）。双方都在对称型 NAT 后面时，ICE 打不通，页面会明确报
  "媒体连接失败：双方都在 NAT 后时需要 TURN（#3）"。
- **接收端目前用自己的 `-stun`/`-turn` 参数**，浏览器用 `/api/ice` 下发的配置。
  STUN 阶段两者独立无所谓；接 TURN 时两边必须用同一份短期凭据，那是 #3 要在
  Hub 上做的事：由 Hub 签发凭据并同时下发给两端。
- **配对码用过即换**：会话结束（发送端断开、接收端重连）后必须重新输码。这是
  一次性配对码的直接后果，也是它安全的原因。
- **`cmd/sender`、`cmd/sender-gui`、`cmd/selfcheck` 仍说旧的局域网协议**，
  它们的目标是已被移除的 `https://<host>:7420/offer`。浏览器页面是当前唯一支持的
  发送端；这几个命令和 `internal/discover` 的去留需要单独决定。
