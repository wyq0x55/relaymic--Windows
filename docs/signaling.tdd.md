# 公网 Signaling TDD 证据（#2）

来源：Issue #2「公网 Signaling：Windows Receiver 只需主动出站」。
用户旅程在本次 TDD 运行中直接推导，没有独立的 plan 文件。

## 用户旅程

1. 作为接收端，我要主动连上公网控制面并拿到一个配对码，这样公司电脑在 NAT
   和防火墙后面也不需要任何入站端口。
2. 作为发送端，我要在浏览器里输入配对码就接到那台机器上，这样我不需要知道它的
   地址，也不需要先进同一个 VPN。
3. 作为运维，我要能确定 6 位配对码不会被在线穷举，也不会在日志里泄漏凭据。

## 任务报告

### 1. 配对状态机（`internal/signaling`）

把配对语义从 WebSocket 后面拿出来单独测：竞态、过期、限流这些最容易出错的地方
不该藏在连接处理里。

- RED：`go test ./internal/signaling` → `undefined: PairingCode / NewPairingCode / Registry / Config`（编译期 RED，测试已引用目标 API）。
- GREEN：`go test -count=1 ./internal/signaling` → `ok`。
- 覆盖率：`go test -race -cover ./internal/signaling` → `88.0% of statements`，竞态干净。

### 2. WSS 控制面（`internal/signaling` 的 server）

- RED：`go test ./internal/signaling` → `undefined: MaxPairingAttemptsPerConn / MaxMessageBytes / PairingFailedText / ICEServer / NewServer`。
- GREEN：`go test -count=1 ./internal/signaling` → `ok`（含 13 个 WebSocket 集成用例）。
- 过程中测试抓到一个真实缺陷：接收端被新连接顶掉后，旧连接走同一个 `defer`
  会 `Release` 掉**新连接**的配对码。修法是只在"自己仍是当前连接"时才作废。
  另一个：顶掉旧连接不能用优雅 `Close`（对方正堵在 `Read` 上，双方互等一个超时），
  改用 `CloseNow`。

### 3. 接收端出站客户端（`internal/hubclient`）

- RED：`go test ./internal/hubclient` → `undefined: Client / New / Config / Handlers`。
- GREEN：`go test -race -count=1 -cover ./internal/hubclient` → `ok`，`82.8% of statements`。

### 4. 配置加载（`internal/signaling` 的 config）

- RED：`go test -count=1 ./internal/signaling` → `undefined: LoadFile`。
- GREEN：`go test -count=1 ./internal/signaling` → `ok`。

### 5. 接收端凭据解析（`cmd/receiver`）

### 6. 过期配对码的补发（`internal/signaling`）

**症状**：接收端连上后拿到的码 5 分钟就过期，而码只在"接收端连上来"和"会话结束"
两处产生。现场慢一步，控制台页面上就再也没有码可给，只能重启接收端。

**RED**：新增 `codejanitor_test.go`，把时钟推过 TTL 后调用 `refreshExpiredCodes`，
断言接收端收到一张不同的 `waiting`。当时 `refreshExpiredCodes` 和 `Registry.LiveCode`
都还不存在，测试编译失败。

**GREEN**：`refreshExpiredCodes` 只处理"还连着、没有会话、码已过期"的接收端，
由 `RunCodeJanitor` 每秒扫一次；`cmd/signaling` 启动它。

- GREEN：`go test -count=1 -tags nolibopusfile ./cmd/receiver` → `ok`。

## 测试规格

| # | 保证什么 | 测试 | 类型 | 结果 |
|---|---|---|---|---|
| 1 | 配对码是 6 位十进制且分布够散 | `internal/signaling/code_test.go:TestNewPairingCodeHasSixDigits` | 单元 | PASS |
| 2 | 用户输入的空格/连字符被收敛，全角数字与长度不符被拒 | `code_test.go:TestNormalizePairingCode` | 单元 | PASS |
| 3 | token 是 256-bit raw url-safe base64，摘要稳定且不等于 token | `token_test.go` | 单元 | PASS |
| 4 | 只有配置里的 token 能通过认证 | `registry_test.go:TestAuthenticateAcceptsOnlyTheConfiguredToken` | 单元 | PASS |
| 5 | 新码作废旧码；码只能兑换一次 | `TestIssueCodeReplacesThePreviousCode`、`TestRedeemConsumesTheCodeOnce` | 单元 | PASS |
| 6 | 过期码在 TTL 边界即失效 | `TestRedeemRejectsExpiredCode` | 单元 | PASS |
| 7 | 未知码与已用码返回**同一个**错误（不给枚举预言机） | `TestRedeemRejectsUnknownCodeWithTheSameError` | 单元 | PASS |
| 8 | 每客户端每分钟 5 次失败后，正确码也被拦；窗口滑过恢复 | `TestRedeemRateLimitsPerClient` | 单元 | PASS |
| 9 | 接收端断线只作废当前码，重连仍可用同一 token | `TestReleaseInvalidatesTheCode` | 单元 | PASS |
| 10 | 并发 IssueCode/Redeem 无竞态 | `TestRegistryIsSafeForConcurrentUse` | 单元 | PASS |
| 11 | 配置里拼错的键直接启动失败 | `TestLoadFileRejectsUnknownFields` | 单元 | PASS |
| 12 | 没有 token 或 token 错误时接收端握手被 401 拒绝 | `server_test.go:TestReceiverWithoutTokenIsRejected` | 集成 | PASS |
| 13 | 外来 Origin 与无 Origin 的发送端被 403 拒绝 | `TestSenderOriginMustBeAllowed` | 集成 | PASS |
| 14 | 配对后 offer/answer 按字节原样转发 | `TestPairingRelaysOfferAndAnswer` | 集成 | PASS |
| 15 | 连续失败到达上限后连接被关闭，但接收端的码没被消费 | `TestWrongCodeUsesOneUniformErrorAndCloses` | 集成 | PASS |
| 16 | 发送端离开 → 接收端收到 `left` 且拿到**不同**的新码 | `TestSenderDisconnectEndsSessionAndIssuesFreshCode` | 集成 | PASS |
| 17 | 接收端离开 → 发送端收到 `closed` 并被断开 | `TestReceiverDisconnectClosesTheSession` | 集成 | PASS |
| 18 | 接收端重连顶掉旧连接，且旧连接断开不作废新码 | `TestReceiverReconnectReplacesTheOldConnection` | 集成 | PASS |
| 19 | 未配对就发 offer 会被断连 | `TestSenderMustPairBeforeSendingAnOffer` | 集成 | PASS |
| 20 | 超过 64 KiB 的消息被断连 | `TestOversizedMessageIsRejected` | 集成 | PASS |
| 21 | ICE 配置经 HTTPS 下发 | `TestICEServersAreServedOverHTTPS` | 集成 | PASS |
| 22 | 客户端把码、joined、offer 上报，并把 answer 发回 | `hubclient/client_test.go:TestRunReportsCodeJoinsAndAnswersOffers` | 集成 | PASS |
| 23 | 控制面掉线后客户端自动重连并拿到新码 | `TestRunReconnectsAfterTheHubDropsTheConnection` | 集成 | PASS |
| 24 | token 错误时 `Run` 立即返回带状态码的错误，不做无谓重试 | `TestRunSurfacesAuthenticationFailure` | 集成 | PASS |
| 25 | ctx 取消后 `Run` 返回 | `TestRunStopsOnContextCancel` | 集成 | PASS |
| 26 | 凭据来源互斥、空文件、缺文件都报错 | `cmd/receiver/main_test.go:TestReadToken` | 单元 | PASS |
| 27 | 码过期后 Hub 自动补发一张**不同**的新码，接收端不必重连 | `codejanitor_test.go:TestExpiredCodeIsReplacedWithoutReconnect` | 集成 | PASS |
| 28 | 码还没过期就不换（换了等于把用户手里那张作废） | 同上（前半段断言 `LiveCode` 未变） | 集成 | PASS |
| 29 | 通话中不补码 | `codejanitor_test.go:TestRefreshLeavesAnActiveCallAlone` | 集成 | PASS |

## 本机端到端验证

在 Windows 上用真实二进制跑通了控制面（不是 mock）：

```powershell
relaymic-signaling -config signaling.json -plain      # 127.0.0.1:8099
relaymic-receiver -hub ws://127.0.0.1:8099/ws/receiver -token-file token.txt -device "CABLE Input"
```

接收端日志：

```text
20:58:22 已连接 ws://127.0.0.1:8099/ws/receiver
20:58:22 设备: CABLE Input (VB-Audio Virtual Cable)
20:58:22 配对码: 427867（5 分钟内有效，用过即换）
20:58:44 发送端已接入
20:58:44 协商失败：设置远端描述: failed to unmarshal SDP: EOF
```

用一个 WebSocket 客户端扮演浏览器（带 `Origin`），配对返回：

```json
{"type":"paired","session":"2bf31ab236c6483ec9743c068166a614","name":"冒烟接收端"}
```

最后那行 `failed to unmarshal SDP` 是预期的：冒烟脚本发的是占位 SDP，不是真
offer。它恰好证明 offer 确实被转发到了接收端并被送去协商。

## 覆盖与已知空白

- `internal/signaling` 88.0%，`internal/hubclient` 82.8%，均带 `-race` 通过。
- **没有覆盖的**：真实浏览器 + 真实 WebRTC 媒体的端到端（需要真麦克风和
  HTTPS 证书）。本机验证只到控制面，媒体面在 #4 做。
- **没有覆盖的**：TURN（#3）。双方都在对称型 NAT 后时当前版本连不上。
- 限流依赖直连地址；放在反向代理后面时由代理负责限流，这一点写在
  `docs/signaling.md`。

## 全套命令

```powershell
go test -count=1 -tags nolibopusfile ./...
go vet  -tags nolibopusfile ./...
go build -tags nolibopusfile ./...
```
