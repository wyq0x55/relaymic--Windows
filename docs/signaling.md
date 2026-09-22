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
   relaymic-receiver.exe -hub wss://mic.example.com/ws/receiver -token-file C:\relaymic\token -monitor 127.0.0.1:7420
   ```

   在 Receiver 本机打开 `http://127.0.0.1:7420/monitor` 查看当前配对码、倒计时、
   链路类型和实时收包统计：

   ```
   一次性配对码 583 921
   剩余 4:59；用过即失效
   ```

2. 你在任何设备的浏览器打开 `https://mic.example.com`，输入 `583921`，
   授权麦克风，点「开始说话」。

3. 音频进入公司电脑的 VB-CABLE，Teams 从 `CABLE Output` 读。

## 协议

控制面只有一条 WebSocket 通道和九种消息，没有"转发到任意目标"这种口子。

| 方向 | 类型 | 说明 |
|---|---|---|
| Hub → 接收端 | `waiting` | 新配对码 + 剩余秒数；码过期时也会重发一张新的 |
| Hub → 接收端 | `joined` | 发送端已用码接入，带上会话标识 |
| Hub → 接收端 | `left` | 发送端离开，随后会发新码 |
| Hub → 发送端 | `paired` | 配对成功，带会话标识和接收端名字 |
| Hub → 发送端 | `closed` | 接收端离线，会话不可继续 |
| Hub → 任一端 | `error` | 可展示的错误（配对失败等） |
| 发送端 → Hub | `pair` | 用配对码认领接收端 |
| 发送端 → Hub → 接收端 | `offer` | 完整 SDP offer，原样搬运 |
| 接收端 → Hub → 发送端 | `answer` | 完整 SDP answer，原样搬运 |
| 接收端 → Hub | `close` | 接收端主动结束当前会话；Hub 踢掉发送端并立刻补发新配对码 |

SDP 是 `json.RawMessage`：控制面不解析、不改写、不缓存，因此不需要跟着 WebRTC
版本升级改代码。单条消息上限 64 KiB，超出直接断开。

`GET /api/ice` 只用 HTTPS 下发公开 STUN 配置。成功配对后，Hub 才会通过双方已建立的
WebSocket 为同一个 session 下发短期 TURN 凭据；公开端点永远不返回 TURN 密码。

## 安全模型

配对码只有 6 位，所以它必须被当成"短期配对能力"，而不是身份认证。

| 面 | 做法 |
|---|---|
| 接收端身份 | 独立 256-bit Bearer token。Hub 只保存 SHA-256 摘要，不保存明文 |
| 配对码 | `crypto/rand` 均匀生成，5 分钟过期，成功即消费，一台接收端同时只有一个活码；过期后 Hub 立刻补发新码，接收端不必重连 |
| 防穷举 | 每客户端每分钟 5 次失败；单条连接 3 次失败即断开 |
| 失败响应 | 未知码、过期码、已用码、被限流返回**同一句话**，不给枚举预言机 |
| 浏览器来源 | 校验 `Origin`，默认只允许同源，可用 `allowedOrigins` 收窄 |
| 转发面 | 只允许 sender→offer、receiver→answer；其余消息类型一律断连 |
| TURN | Hub 从受限文件读取 coturn REST API 共享密钥；按 session 签发 10 分钟 HMAC 凭据，且只在配对后下发给双方 |
| 日志 | 不记录 token、配对码、原始 SDP、ICE 候选或对端 IP |

接收端重连时旧连接会被立即顶掉（`CloseNow`），避免两个会话同时挂同一台机器；
旧连接断开不会作废新连接的配对码。

## 部署

### 0. 管理面（可选，但推荐）

默认没有管理面：加一个人要改这份配置再重启 Hub，重启会掐断正在通话的人。
配齐下面两项就会开 `/admin`：

```bash
relaymic-signaling -gen-admin-token > /etc/relaymic/admin-token
sudo chmod 600 /etc/relaymic/admin-token
sudo chown relaymic:relaymic /etc/relaymic/admin-token
```

```json
{
  "adminTokenFile": "/etc/relaymic/admin-token",
  "receiversFile": "/var/lib/relaymic/receivers.json"
}
```

- 浏览器打开 `https://<你的 Hub>/admin`，用口令登录（口令存进 HttpOnly cookie，
  页面脚本拿不到），在页面上生成 token、看谁在线、注销。
- 脚本/命令行用 Bearer：
  `curl -H "Authorization: Bearer <口令>" https://<你的 Hub>/admin/api/receivers`
- `receiversFile` 只存 token 摘要：这份文件被读走也不等于交出可用凭据。
- `receiversFile` 的目录要对服务可写。systemd 里开了 `ProtectSystem=strict` 时，
  `/var/lib` 默认只读 —— 加 `StateDirectory=relaymic`，或把它指到已经可写的目录
  （例如 `/opt/relaymic/receivers.json`）。不然生成 token 时会报
  `read-only file system`。
- 目录写不进去时：Hub 启动会在日志里打一条
  `警告: 管理面生成 token 会失败 —— ...`（但不会因此起不来，已经在连的人不受影响），
  页面上生成 token 会回 500，正文直接写清该改 unit 还是改配置。
- 生成出来的 token 只在创建那一刻显示一次；对方写进自己的 `receiver-token.txt`
  就能连，**不用重启 Hub**。
- 注销当场生效：连接被断开，token 立刻失效。
- 口令泄漏等于管理面失守。它是公网上的一个面，登录口有每分钟 8 次的失败限流，
  但仍然：别复用别的口令，别写进脚本历史。

配了管理面之后 `receivers` 可以为空 —— 加人从页面上走。

### 1. 生成接收端凭据

```bash
relaymic-signaling -gen-receiver 公司电脑
```

一次给多台机器生成就重复这个参数，输出直接就是 `receivers` 数组：

```bash
relaymic-signaling -gen-receiver 张三-PC -gen-receiver 李四-PC
```

```json
[
  {"name": "张三-PC", "token": "..."},
  {"name": "李四-PC", "token": "..."}
]
```

token 只在这里出现一次，请存到各自接收端的 `-token-file` 里（文件权限就是凭据的
权限边界；命令行参数会出现在进程列表里）。每台机器一份 token，互不影响：
Hub 按接收端隔离配对码和会话，多台机器可以同时各自通话。

只要接收端连着、又没人配对，它名下就一直有一张活码：5 分钟到期后 Hub 会补发
新的（通话中不补 —— 那会儿它不在等人配对）。否则现场慢一步，页面上就再也没有
码可给，只能去重启接收端。

### 2. 配置文件

```json
{
  "listen": ":443",
  "certFile": "/etc/letsencrypt/live/mic.example.com/fullchain.pem",
  "keyFile": "/etc/letsencrypt/live/mic.example.com/privkey.pem",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [{"urls": ["stun:stun.l.google.com:19302"]}],
  "turn": {
    "urls": ["turn:turn.example.com:3478?transport=udp"],
    "authSecretFile": "/etc/relaymic/turn-auth-secret",
    "credentialTTLSeconds": 600
  },
  "receivers": [{"name": "公司电脑", "token": "<32 字节 raw url-safe base64>"}]
}
```

配置里拼错的键会直接启动失败（`DisallowUnknownFields`）：把 `receivers` 写成
`receiver` 却静默忽略，等于上线后才发现一台机器都连不上。`iceServers` 只接受
无凭据的 STUN；把静态 TURN 地址或密码放进去会被拒绝，避免 `/api/ice` 泄露中继权限。

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
| `-monitor` | 本机控制台监听地址，例如 `127.0.0.1:7420`；显示配对码、状态和链路统计；**留空表示不监听任何端口** |
| `-device` | 虚拟音频设备；Windows 默认 `CABLE Input` |

当 Hub 配了 `turn` 时，Receiver 会在成功配对后自动应用短期会话 ICE 配置；不需要也
不应把 `-turn-user` / `-turn-pass` 写入命令行。`-force-relay` 只用于验证 coturn 路径。

接收端没有任何入站监听。要看本机控制台就显式开 `-monitor`；配对码只会在
`127.0.0.1` / `::1` 访问时返回，其他地址的页面只显示已隐藏，避免把短期能力暴露到局域网。

## 本机自测

```powershell
relaymic-signaling -config signaling.json -addr 127.0.0.1:8099 -plain
relaymic-receiver -hub ws://127.0.0.1:8099/ws/receiver -token-file token.txt -device "CABLE Input"
```

`-plain` 只用于本机：浏览器只在安全上下文里给麦克风权限，所以真正的发送端页面必须走
HTTPS。`-self-signed-dir <目录>` 可以在没有证书的情况下用自签证书把 TLS 链路跑通。

## 当前边界

- coturn 必须启用 REST API 认证（`use-auth-secret` / `static-auth-secret`），并与
  `turn.authSecretFile` 使用同一个共享密钥。Hub 不代管或安装 coturn。
- 部署后要用 `-force-relay` 跑一轮真实双端通话；只有这个验证通过，才能声明对称 NAT
  支持已完成。
- **配对码用过即换**：会话结束（发送端断开、接收端重连）后必须重新输码。这是
  一次性配对码的直接后果，也是它安全的原因。
- **`cmd/sender`、`cmd/sender-gui`、`cmd/selfcheck` 仍说旧的局域网协议**，
  它们的目标是已被移除的 `https://<host>:7420/offer`。浏览器页面是当前唯一支持的
  发送端；这几个命令和 `internal/discover` 的去留需要单独决定。

## 只放行 HTTP 代理的网络（TURN 隧道）

有些公司网络不允许任何直连出网，只放行一个 HTTP 代理：DNS、STUN、TURN 的 UDP/TCP
全部打不通，只有走代理的 HTTPS/WSS 能出去。这种情况下 TURN 永远拿不到中继地址 ——
TURN 客户端不会使用 HTTP 代理。

Hub 可以开一个隧道端点，让 Receiver 把 TURN/TCP 塞进它已经能用的那条 WSS：

1. Hub 配置加 `turn.tunnelTarget`，指向 coturn 的本机 TCP 监听地址：

   ```json
   "turn": {
     "urls": ["turn:turn.example.com:3478?transport=udp"],
     "authSecretFile": "/etc/relaymic/turn-auth-secret",
     "tunnelTarget": "127.0.0.1:3478"
   }
   ```

2. Receiver 加 `-turn-tunnel`。它在回环地址上开一个 TCP 入口，把 ICE 里的 TURN 地址
   换成本机入口（`turn:127.0.0.1:<port>?transport=tcp`），每条连接经 `/ws/tunnel`
   隧道到 Hub，再由 Hub 转给 coturn。

边界：

- 隧道只对通过 token 认证的 Receiver 开放，且只能转发到配置里写死的那个地址。
- 音频仍是端到端 DTLS-SRTP 加密：Hub 只看到 TURN 协议字节，看不到内容。
- 远端浏览器不受影响，照常用 Hub 下发的原始 TURN 地址。
- coturn 要在本机监听 TCP（默认 `listening-port=3478` 同时听 UDP 和 TCP），
  且 `listening-ip` 要包含 `127.0.0.1`，否则隧道连不上。
- 没配 `tunnelTarget` 时端点返回 404；Receiver 不带 `-turn-tunnel` 时行为与之前完全一致。
