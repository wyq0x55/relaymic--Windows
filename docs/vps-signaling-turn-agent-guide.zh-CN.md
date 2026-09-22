# RelayMic VPS Signaling 与 TURN 部署指导（交给执行 Agent）

> 目标：在一台已有公网 IPv4 的 Ubuntu VPS 上部署 RelayMic Signaling 与 coturn，
> 为 Windows Receiver 和浏览器发送端提供 HTTPS/WSS 会合点，并为后续受限 NAT
> 场景准备 TURN。本文不授权修改现有 VPN、3x-ui、Hysteria、SSH 或防火墙以外的服务。

## 先读：当前代码边界

`cmd/signaling` 已能用于生产：浏览器和 Receiver 都主动连接同一个 HTTPS/WSS
入口，音频不经过 Signaling。

**不要把 coturn 的长期用户名/密码放进 `signaling.json` 的 `iceServers`。** 当前
`GET /api/ice` 可被任何打开站点的人读取；长期 TURN 凭据一旦出现在这里，任何人
都能拿它消耗你的中继流量。当前代码在配对成功后给双方下发同一组短期 TURN 凭据，
因此部署分两阶段：

1. 完成 Signaling 的生产部署，并安装、收紧、外部验证 coturn。
2. 部署包含短期凭据功能的 RelayMic commit，配置受限共享密钥文件后，用
   `-force-relay` 验证真实双向中继。

不要把“coturn 服务已启动”说成“RelayMic 已能在对称 NAT 下中继通话”。后者必须通过
`-force-relay` 的真实双端验证。

## Agent 任务契约

开始前向用户索取或确认以下值；**不索取 token、私钥或 root 密码的明文到聊天记录**：

| 变量 | 示例 | 用途 |
| --- | --- | --- |
| `VPS_HOST` | `203.0.113.10` | SSH 主机或别名 |
| `SIGNAL_FQDN` | `mic.example.com` | Signaling 的 A/AAAA 记录 |
| `TURN_FQDN` | `turn.example.com` | coturn 的 A/AAAA 记录，可与前者相同 |
| `RELAYMIC_REPO` | `https://github.com/wyq0x55/relaymic--Windows.git` | 包含已审查功能的 Git 仓库 |
| `RELAYMIC_REF` | 已审查的 commit SHA | 要部署的 RelayMic 版本 |
| `RECEIVER_NAME` | `company-windows` | Receiver 的稳定显示名 |

执行约束：

1. 先只读盘点，再修改；若 `80`、`443`、`3478`、`5349` 或 `49160-49200/udp` 已被占用，停下并报告占用者。
2. 不停止、不重启、不改写已有 3x-ui、Hysteria、sing-box、Tailscale 或 SSH 服务；端口冲突时由用户选择方案。
3. 所有秘密只写入 root/服务账户可读的文件，文件模式 `0600`；不打印到终端记录、systemd 状态、Git 或聊天。
4. 每一阶段都先验证再继续；任何外部 DNS、UFW/云安全组变更都在执行前列出精确端口与理由。

## 第 0 步：只读盘点

登录 VPS 后先执行以下命令并汇报摘要（不要贴出私钥、完整配置或密码）：

```bash
set -euo pipefail
uname -m
cat /etc/os-release
hostnamectl
ip -brief address
sudo ss -lntup
sudo systemctl --type=service --state=running --no-pager
sudo ufw status verbose || true
sudo nft list ruleset || true
```

同时在 DNS 侧确认 `SIGNAL_FQDN` 与 `TURN_FQDN` 解析到此 VPS 公网地址。若使用 AAAA，
VPS 必须真的有可达 IPv6；否则不要创建 AAAA 记录。确认以下计划端口没有冲突：

| 端口 | 协议 | 服务 | 原因 |
| --- | --- | --- | --- |
| `80` | TCP | Caddy | ACME HTTP-01 证书申请与 HTTPS 跳转 |
| `443` | TCP | Caddy | 浏览器 HTTPS 与 WSS Signaling |
| `3478` | UDP + TCP | coturn | STUN/TURN 分配 |
| `49160-49200` | UDP | coturn | 实际中继数据端口段 |

`5349`（TURN/TLS）只在已为 coturn 单独准备证书、确认无端口冲突且确有 TCP-only
网络需求时再添加；不要让 Caddy 代理 UDP/TURN。

## 第 1 步：安装运行环境与固定版本

以下示例以 Ubuntu 24.04 和 `amd64` 为例；非 `amd64` 或非 Debian 系发行版先停下，
改用对应包管理器与 Go 官方发行包。不要使用发行版陈旧的 Go 替代项目要求的版本。

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git caddy coturn ufw

go version || true
```

若 `go version` 低于仓库 `go.mod` 声明的版本，从 `https://go.dev/dl/` 下载匹配架构的
**官方** Go 压缩包；先在官方下载清单核对 SHA-256，再安装到 `/usr/local/go`。安装后，
新 shell 必须满足：

```bash
export PATH=/usr/local/go/bin:$PATH
go version
```

创建最小权限服务账户与目录：

```bash
sudo useradd --system --home /opt/relaymic --shell /usr/sbin/nologin relaymic \
  2>/dev/null || true
sudo install -d -o relaymic -g relaymic -m 0750 /opt/relaymic /etc/relaymic
```

检出用户指定的不可变 commit，不要部署本地未提交改动或浮动分支头：

```bash
sudo -u relaymic git clone "$RELAYMIC_REPO" /opt/relaymic/src
sudo -u relaymic git -C /opt/relaymic/src checkout --detach "$RELAYMIC_REF"
sudo -u relaymic git -C /opt/relaymic/src status --porcelain
sudo -u relaymic env PATH=/usr/local/go/bin:$PATH \
  go -C /opt/relaymic/src build -trimpath -buildvcs=true \
  -o /opt/relaymic/relaymic-signaling ./cmd/signaling
sudo chown relaymic:relaymic /opt/relaymic/relaymic-signaling
sudo chmod 0755 /opt/relaymic/relaymic-signaling
```

`git status --porcelain` 必须为空。若仓库地址或 commit 与用户提供的不一致，停止并报告，
不要猜测分支。

## 第 2 步：部署 HTTPS/WSS Signaling

使用 Caddy 终止 TLS，RelayMic 只监听回环 HTTP。这样 RelayMic 无需读取证书私钥，
也不会以 root 身份绑定 `443`。Caddy 原生支持 WebSocket 反向代理。

创建 `/etc/relaymic/signaling.json`。`iceServers` 只放公开 STUN；TURN 共享密钥只从
受限文件读取：

```json
{
  "listen": "127.0.0.1:8080",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [
    {"urls": ["stun:stun.cloudflare.com:3478"]}
  ],
  "turn": {
    "urls": ["turn:turn.example.com:3478?transport=udp"],
    "authSecretFile": "/etc/relaymic/turn-auth-secret",
    "credentialTTLSeconds": 600
  },
  "receivers": [
    {"name": "company-windows", "token": "REPLACE_WITH_GENERATED_TOKEN"}
  ],
  "adminTokenFile": "/etc/relaymic/admin-token",
  "receiversFile": "/var/lib/relaymic/receivers.json"
}
```

### 管理面（可选）

配齐 `adminTokenFile` 和 `receiversFile` 之后 `/admin` 才存在；两项都不配就是
404。开了以后加人不用改配置重启（重启会掐断正在通话的人）。

```bash
sudo -u relaymic /opt/relaymic/relaymic-signaling -gen-admin-token > /root/relaymic-admin-token
sudo install -o relaymic -g relaymic -m 0600 /root/relaymic-admin-token /etc/relaymic/admin-token
sudo rm /root/relaymic-admin-token   # 抄给用户之后
```

`receiversFile` 所在的目录必须对服务可写。unit 里有 `ProtectSystem=strict`，
**只有 `ReadWritePaths` 和 systemd 自己管的目录可写**，`/var/lib` 默认是只读的 ——
不处理就会在页面上生成 token 时报 `read-only file system`。两种做法选一个：

- 给 unit 加 `StateDirectory=relaymic`（systemd 会建好 `/var/lib/relaymic` 并自动
  允许写；推荐）；
- 或者把 `receiversFile` 指到已经可写的目录，例如 `/opt/relaymic/receivers.json`。

`allowedOrigins` 只填 host，不填 `https://`。令牌生成方式如下；输出只进入受限文件：

```bash
sudo umask 077
sudo -u relaymic /opt/relaymic/relaymic-signaling \
  -gen-receiver "$RECEIVER_NAME" > /root/relaymic-receiver-bootstrap.json
sudo chmod 0600 /root/relaymic-receiver-bootstrap.json
```

由 Agent 从该 JSON 中**仅在服务器内**取出 `token` 写入 `signaling.json` 的同名
Receiver 项，随后设置：

```bash
sudo chown relaymic:relaymic /etc/relaymic/signaling.json
sudo chmod 0600 /etc/relaymic/signaling.json
```

保留 `/root/relaymic-receiver-bootstrap.json` 直到 Windows Receiver 已安全保存 token；
成功后由用户确认再删除。绝不把 token 作为 `-token` 命令行参数使用。

写入 `/etc/systemd/system/relaymic-signaling.service`：

```ini
[Unit]
Description=RelayMic signaling service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=relaymic
Group=relaymic
ExecStart=/opt/relaymic/relaymic-signaling -config /etc/relaymic/signaling.json -plain
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/relaymic
# 管理面的接收端清单要落盘；systemd 会建好这个目录并允许写。
StateDirectory=relaymic

[Install]
WantedBy=multi-user.target
```

写入 Caddy 站点配置（将域名替换为实际 `SIGNAL_FQDN`）：

```caddyfile
mic.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

配置位置取决于现有 Caddy 布局。Agent 必须先 `caddy validate`，再 reload，不能覆盖
已有站点：

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl daemon-reload
sudo systemctl enable --now relaymic-signaling
sudo systemctl reload caddy
sudo systemctl status relaymic-signaling caddy --no-pager
curl --fail --silent --show-error https://$SIGNAL_FQDN/api/ice
```

如 `443` 已被 Nginx、Caddy 或其他服务占用，不要替换它。先报告现有 TLS 终止服务和
相关站点配置，再由用户选择复用现有反向代理或使用新的域名/端口。

## 第 3 步：安装和收紧 coturn

生成一个仅保存在 VPS 的随机 TURN REST API 共享密钥：

```bash
sudo install -d -o root -g relaymic -m 0750 /etc/relaymic
sudo sh -c 'umask 077; openssl rand -base64 48 > /etc/relaymic/turn-auth-secret'
sudo chown root:relaymic /etc/relaymic/turn-auth-secret
sudo chmod 0640 /etc/relaymic/turn-auth-secret
```

创建 `/etc/turnserver.conf`。将 `TURN_PUBLIC_IP`、`TURN_FQDN` 替换为真实值；若 VPS
本身直接持有公网 IP，`external-ip` 填同一个 IP。若前面有 1:1 NAT，使用
`external-ip=PRIVATE_IP/PUBLIC_IP`，并先确认提供商网络映射。

```ini
listening-port=3478
listening-ip=TURN_PUBLIC_IP
relay-ip=TURN_PUBLIC_IP
external-ip=TURN_PUBLIC_IP
realm=turn.example.com
server-name=turn.example.com

fingerprint
lt-cred-mech
use-auth-secret
static-auth-secret=REPLACE_WITH_CONTENTS_OF_SECRET_FILE
stale-nonce
no-cli
no-tlsv1
no-tlsv1_1
no-loopback-peers
no-multicast-peers
min-port=49160
max-port=49200
user-quota=12
total-quota=48
```

`static-auth-secret` 必须与 `/etc/relaymic/turn-auth-secret` 内容完全相同。让 root 用
文件重定向写入 coturn 配置，不要把密钥放进命令行、Git、截图、聊天或 RelayMic JSON。
`turnserver.conf` 仍为 `0600`，而共享密钥文件仅允许 `root` 和 `relaymic` 组读取。配置后：

```bash
sudo chmod 0600 /etc/turnserver.conf
sudo systemctl enable --now coturn
sudo systemctl status coturn --no-pager
sudo ss -lntup | grep -E ':(3478|49160|49161)\\b' || true
```

打开云安全组及本机防火墙时只开放以下端口：

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 3478/tcp
sudo ufw allow 3478/udp
sudo ufw allow 49160:49200/udp
sudo ufw status numbered
```

若 UFW 未启用，记录这一事实并检查 nftables/云安全组；不要为了“统一”而清空、替换或
启用未知的防火墙策略。

### 外部 TURN 验证

必须从 VPS 外的网络执行验证，VPS 本机成功不代表云防火墙和中继端口段可达。

1. 在 VPS 上根据 coturn REST API 为 10 分钟有效期生成一个一次性 `username` / `password`。
   `username` 格式为 `<unix-expiry>:relaymic-smoke`，密码为共享密钥对用户名的 HMAC-SHA1
   再 Base64 编码。只把这对短期值交给验证机；永不交出共享密钥。
2. 在 Windows 的 RelayMic 检出中构建并运行：

   ```powershell
   go run ./cmd/turncheck -server "$TURN_FQDN`:3478" `
     -user '<短期用户名>' -pass '<短期密码>' -realm "$TURN_FQDN"
   ```

3. 只有输出同时包含 `STUN 绑定 ✓`、`TURN 分配 ✓` 与 `中继转发 ✓`，才算 coturn 数据面
   通过；分配成功而转发失败通常意味着 `49160-49200/udp` 未放行。
4. 验证完成后使该凭据自然过期；不复用它作为生产凭据。

## 第 4 步：接入 RelayMic 并验收

1. 将 `RELAYMIC_REF` 固定到包含短期 TURN 凭据功能的 commit，构建并替换 Hub 二进制。
2. 保持 `iceServers` 只含 STUN，配置上方的 `turn` 对象；`authSecretFile` 必须能由
   `relaymic` 服务账户读取。
3. 重启 Hub 后，`/api/ice` 仍只能返回 STUN，绝不能出现 TURN 用户名或密码。
4. 配对成功后，Hub 会把同一组 10 分钟 coturn REST API 凭据下发给浏览器和 Receiver；
   Receiver 不再要求 `-turn-user` / `-turn-pass`。
5. 在 Windows Receiver 上加 `-force-relay`，完成一次浏览器麦克风和 Teams 回传的双向
   通话。只有成功才可以宣布对称 NAT 支持完成。

## 第 5 步：交付验收与 Windows 启动

部署 Agent 最终只报告以下不敏感证据：

1. 域名解析、`https://SIGNAL_FQDN/api/ice`、`systemctl is-active relaymic-signaling caddy coturn` 的结果。
2. 实际监听端口、UFW/云安全组中新增的精确规则，以及没有触碰的既有服务。
3. 外网 `turncheck` 的三项成功标记（不带用户名和密码）。
4. RelayMic commit SHA、二进制 SHA-256、systemd 单元与配置文件权限。
5. 未完成项：真实 `-force-relay` E2E，除非已执行并给出对应证据。

Receiver token 应通过受控渠道复制到 Windows 上一个仅当前用户可读的文件，例如
`C:\\relaymic\\receiver-token.txt`。随后运行：

```powershell
C:\\workspeace\\relaymic\\bin\\relaymic-receiver.exe `
  -hub "wss://SIGNAL_FQDN/ws/receiver" `
  -token-file "C:\\relaymic\\receiver-token.txt" `
  -device "CABLE Input" `
  -return-device "VoiceMeeter Aux Output" `
  -monitor "127.0.0.1:7420" `
  -force-relay
```

在 Receiver 电脑上打开 `http://127.0.0.1:7420/monitor` 查看一次性配对码；它只在本机
回环地址显示，不会写入运行日志。随后在任意设备打开 `https://SIGNAL_FQDN`，输入配对码，授权
麦克风。先确认直连路径；再用 `-force-relay` 验证经 coturn 的双向通话，最后宣布公网
受限网络支持完成。

## 只放行 HTTP 代理的公司网络

若 Receiver 所在网络只放行 HTTP 代理（DNS/STUN/TURN 的 UDP 与直连 TCP 全部不通），
按上面的常规接法一定失败：TURN 客户端不会使用代理。改用 TURN 隧道：

1. Hub 的 `signaling.json` 里给 `turn` 加一项，指向 coturn 本机 TCP 监听：

   ```json
   "tunnelTarget": "127.0.0.1:3478"
   ```

   同时确认 coturn 的 `listening-ip` 包含 `127.0.0.1`（只绑公网 IP 时隧道连不上）。

2. Receiver 启动参数加 `-turn-tunnel`，其余不变：

   ```powershell
   relaymic-receiver.exe -hub "wss://SIGNAL_FQDN/ws/receiver" `
     -token-file "C:\relaymic\receiver-token.txt" `
     -device "CABLE Input" -return-device "VoiceMeeter Aux Output" `
     -monitor "127.0.0.1:7420" -turn-tunnel -force-relay
   ```

3. 验收：Receiver 日志出现 `TURN 隧道: 127.0.0.1:<port> → wss://...`，配对后链路显示
   为 relay，且音频双向可通。隧道走的是 9443 的 WSS，不需要额外放行端口。

注意：这只解决 Receiver 侧。发送端（浏览器）若也在同样的受限网络里，仍需各自的出口。
