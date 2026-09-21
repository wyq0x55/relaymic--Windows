# RelayMic VPS Signaling 与 TURN 部署指导（交给执行 Agent）

> 目标：在一台已有公网 IPv4 的 Ubuntu VPS 上部署 RelayMic Signaling 与 coturn，
> 为 Windows Receiver 和浏览器发送端提供 HTTPS/WSS 会合点，并为后续受限 NAT
> 场景准备 TURN。本文不授权修改现有 VPN、3x-ui、Hysteria、SSH 或防火墙以外的服务。

## 先读：当前代码边界

`cmd/signaling` 已能用于生产：浏览器和 Receiver 都主动连接同一个 HTTPS/WSS
入口，音频不经过 Signaling。

**不要把 coturn 的长期用户名/密码放进 `signaling.json` 的 `iceServers`。** 当前
`GET /api/ice` 可被任何打开站点的人读取；长期 TURN 凭据一旦出现在这里，任何人
都能拿它消耗你的中继流量。当前代码尚未实现「配对成功后给双方下发同一组短期 TURN
凭据」的运行时链路，因此本次部署分两阶段：

1. 完成 Signaling 的生产部署，并安装、收紧、外部验证 coturn。
2. 仅在短期凭据代码和测试合入后，才把 TURN 暴露给 RelayMic 浏览器与 Receiver。

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

创建 `/etc/relaymic/signaling.json`，初始只配置 STUN；这里不放 TURN 密码：

```json
{
  "listen": "127.0.0.1:8080",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [
    {"urls": ["stun:stun.cloudflare.com:3478"]}
  ],
  "receivers": [
    {"name": "company-windows", "token": "REPLACE_WITH_GENERATED_TOKEN"}
  ]
}
```

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

## 第 3 步：安装和收紧 coturn（尚不下发给客户端）

生成一个仅保存在 VPS 的随机 TURN REST API 共享密钥：

```bash
sudo install -d -o root -g root -m 0700 /etc/turnserver
sudo sh -c 'umask 077; openssl rand -base64 48 > /etc/turnserver/relaymic-auth-secret'
sudo chmod 0600 /etc/turnserver/relaymic-auth-secret
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

`static-auth-secret` 的内容只由 root 读取并写入 root 可读的配置，权限必须为 `0600`。
不要把它提交、截图、粘贴到聊天或放入 RelayMic JSON。配置后：

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

## 第 4 步：TURN 接入 RelayMic 的开发门槛

安装 coturn 后，**不要**把静态用户名/密码加入 `iceServers`。让开发 Agent 先实现并
测试以下能力，然后才能启用中继：

1. Hub 从 root/服务账户受限文件读取 coturn REST API 共享密钥；配置文件和日志中不出现该密钥。
2. 每次成功配对后，Hub 为该 session 生成短期 HMAC TURN 凭据，并将**同一对**凭据发给
   已认证 Receiver 与已配对 Sender。
3. Sender 页面在配对成功后才创建 `RTCPeerConnection` 并使用短期 ICE 配置；不能在公开
   `/api/ice` 中泄露长期或可长期滥用的 TURN 凭据。
4. Receiver 在收到同 session 的 ICE 配置后、生成 answer 前应用它；不再要求用户将
   `-turn-user` / `-turn-pass` 写进 Windows 命令行。
5. 测试覆盖：未配对无法取得 TURN 凭据、两端获得同一到期凭据、过期凭据被 coturn 拒绝、
   重连/换码不能复用旧凭据，以及真实双端 `-force-relay` 音频收发。

在第 4 步合入前，Signaling 的 `iceServers` 保持仅 STUN；coturn 可保留为已安装且独立
验证过的基础设施，但不得声称已用于 RelayMic 通话。

## 第 5 步：交付验收与 Windows 启动

部署 Agent 最终只报告以下不敏感证据：

1. 域名解析、`https://SIGNAL_FQDN/api/ice`、`systemctl is-active relaymic-signaling caddy coturn` 的结果。
2. 实际监听端口、UFW/云安全组中新增的精确规则，以及没有触碰的既有服务。
3. 外网 `turncheck` 的三项成功标记（不带用户名和密码）。
4. RelayMic commit SHA、二进制 SHA-256、systemd 单元与配置文件权限。
5. 未完成项：短期 TURN 凭据代码与真实 `-force-relay` E2E，除非已执行并给出对应证据。

Receiver token 应通过受控渠道复制到 Windows 上一个仅当前用户可读的文件，例如
`C:\\relaymic\\receiver-token.txt`。随后运行：

```powershell
C:\\workspeace\\relaymic\\bin\\relaymic-receiver.exe `
  -hub "wss://SIGNAL_FQDN/ws/receiver" `
  -token-file "C:\\relaymic\\receiver-token.txt" `
  -device "CABLE Input" `
  -return-device "VoiceMeeter Aux Output"
```

Receiver 打印一次性配对码后，在任意设备打开 `https://SIGNAL_FQDN`，输入配对码，授权
麦克风。验证时先确认直连路径；短期凭据功能合入后，额外用 `-force-relay` 验证经 coturn
的双向通话，再宣布公网受限网络支持完成。
