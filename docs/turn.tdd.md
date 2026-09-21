# TURN 短期凭据 TDD 证据

本轮需求来自真实公网联调：双方 NAT 后仅有 STUN，媒体连接失败。目标是在不公开
coturn 长期共享密钥或静态中继密码的前提下，为每次成功配对提供同一组短期凭据。

## 用户旅程

1. 作为已配对的浏览器和 Receiver，我获得同一组仅绑定当前会话的 coturn REST API
   凭据，因此可以在强制中继模式下协商媒体。
2. 作为未配对的访问者，我从公开 `/api/ice` 只能看到无凭据 STUN，不能取得 TURN
   中继权限。
3. 作为运维者，我把 coturn 共享密钥保存在受限文件中，Hub 启动时读取它，而不是把
   密钥或派生凭据写入 JSON、命令行或日志。

## RED

先新增 `internal/signaling/turn_test.go`、配置拒绝测试、Hub 会话下发测试和 Receiver
ICE 映射测试，再运行：

```powershell
go test -tags nolibopusfile ./internal/signaling ./internal/hubclient ./cmd/receiver
```

结果为预期 RED：`NewTurnIssuer`、`Server.turnIssuer`、`Message.ICEServers` 和
`hubICEServers` 尚不存在，测试编译失败。该检查点已记录在 commit `eb4cfed`。

## GREEN

实现 `TurnIssuer`（HMAC-SHA1 coturn REST API 兼容格式）、`turn.authSecretFile` 配置、
配对消息中的 session ICE 配置以及浏览器/Receiver 的协商前应用后，执行：

```powershell
go test -cover -tags nolibopusfile ./internal/signaling ./internal/hubclient ./cmd/receiver
go test -race -tags nolibopusfile ./...
go vet -tags nolibopusfile ./internal/signaling ./internal/hubclient ./cmd/signaling ./cmd/receiver
```

结果：

| 保证 | 测试 | 结果 |
| --- | --- | --- |
| 同一 session 得到确定的 10 分钟 REST 用户名与 HMAC 凭据 | `TestTurnIssuerCreatesSessionBoundRESTCredentials` | PASS |
| 空密钥、非 TURN URL、空地址和无效 TTL 被拒绝 | `TestNewTurnIssuerRejectsUnsafeConfiguration` | PASS |
| 公共 `iceServers` 拒绝静态 TURN 密码 | `TestLoadFileRejectsLongLivedTURNCredentialsInPublicICEConfig` | PASS |
| Hub 从受限文件读共享密钥 | `TestTurnIssuerReadsSecretOnlyFromRestrictedFile` | PASS |
| 成功配对的浏览器和 Receiver 得到同一 STUN+TURN 配置 | `TestPairedEndpointsReceiveSameEphemeralTURNCredentials` | PASS |
| `/api/ice` 不泄露动态凭据 | `TestPublicICEEndpointDoesNotExposeSessionTURNCredentials` | PASS |
| Receiver 客户端接收并保留 session ICE 凭据 | `TestRunPassesSessionICEServersToReceiver`、`TestHubICEServersPreservesSessionTURNCredentials` | PASS |

覆盖率：`internal/signaling` 88.5%，`internal/hubclient` 82.8%。`cmd/receiver` 是带
真实音频设备初始化的可执行入口，包总覆盖率为 4.9%；本次新增的纯转换函数已由单测覆盖，
而完整媒体路径由全量竞态测试覆盖。

## 未完成的外部验证

代码和本地协议测试不等于 coturn 已可用。部署后仍必须从两张真实 NAT 网络运行
Receiver 的 `-force-relay`，确认浏览器麦克风和 Teams 回传均经中继收发成功；这将验证
3478 和中继 UDP 端口段的云防火墙规则。
