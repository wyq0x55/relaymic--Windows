# 本机控制台 TDD 证据

本轮从真实的 Receiver 使用流程得出：配对码不应只出现在终端，操作者需要在公司电脑
本机看到配对倒计时和当前链路状态。配置摘要必须是只读的，不能显示 Receiver token。

## 用户旅程

1. 作为 Receiver 操作者，我在本机控制台查看并复制当前一次性配对码及其倒计时。
2. 作为把监控页错误暴露到局域网的操作者，我的远程浏览器看不到配对码。
3. 作为排障人员，我同时看到当前 Hub、音频设备、回传和直连/TURN 链路状态。

## RED

先新增 `internal/monitor/pairing_test.go`，再执行：

```powershell
go test ./internal/monitor
```

预期因 `NewPairingState` 和 `IsLoopbackRemoteAddr` 尚不存在而编译失败。

## GREEN

实现后执行：

```powershell
go test -race -cover ./internal/monitor ./internal/signaling ./internal/hubclient
go vet ./internal/monitor ./internal/signaling ./internal/hubclient ./cmd/signaling
```

| 保证 | 测试 | 结果 |
| --- | --- | --- |
| 本机读取活动配对码与剩余时间 | `TestPairingStateShowsActiveCodeOnlyToLocalViewer` | PASS |
| 非回环访问被隐藏，已用或过期码为空 | `TestPairingStateShowsActiveCodeOnlyToLocalViewer`、`TestPairingStateClearsUsedAndExpiredCode` | PASS |
| 仅 IPv4/IPv6 回环地址可取得配对码 | `TestIsLoopbackRemoteAddr` | PASS |

Windows 原生验证使用 MSYS2 的 `mingw-w64-x86_64-opus`，并设置其 `pkgconfig` 与 `bin`
目录后执行 `go test -race -cover -tags nolibopusfile ./cmd/receiver`，结果通过。
覆盖率：`internal/monitor` 为 100.0%，`cmd/receiver` 为 4.8%。
