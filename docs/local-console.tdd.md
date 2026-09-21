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

## 第二轮：链路模式与最近日志

排障时"连不上"和"连上了但走哪条路"是两个问题。候选类型回答不了后者：开着 TURN
隧道时本端看到的是回环地址上的中继候选，和"对端在公网中继上"完全不是一回事。
所以链路模式由 Receiver 自己判定，页面只负责显示。

同时把日志接一份进内存：页面上看到的和日志里写的是同一份事实，不用再靠"哪处记得
记事件"。

### RED

先写 `internal/rtc/receiver_test.go` 的 `TestClassifyPathNamesTheMode`、
`TestPathTextNamesBothEnds`、`TestPathTextOnAnEmptyPath`，以及
`internal/monitor/eventlog_test.go`，再执行：

```powershell
go test ./internal/rtc ./internal/monitor
```

预期因 `Path`、`classifyPath`、`NewEventLog` 尚不存在而编译失败。

### GREEN

```powershell
go test -race -count=1 -tags nolibopusfile ./...
go vet -tags nolibopusfile ./...
```

| 保证 | 测试 | 结果 |
| --- | --- | --- |
| 直连 / TURN 中继 / 隧道中继三种模式 | `TestClassifyPathNamesTheMode` | PASS |
| 隧道开着但这一对候选是直连时仍报直连 | `TestClassifyPathNamesTheMode`（"隧道开着却直连"） | PASS |
| 未建立时给人话而不是空白 | `TestPathTextOnAnEmptyPath` | PASS |
| 只留最近 N 行 | `TestEventLogKeepsTheMostRecentLines` | PASS |
| 写入被切开时半行不丢、两行不粘 | `TestEventLogReassemblesSplitWrites` | PASS |
| 半行一直不换行也会露面 | `TestEventLogSurfacesAStuckPartialLine` | PASS |
| 多协程写日志安全 | `TestEventLogIsSafeForConcurrentWriters` | PASS |
| `Recent()` 不把内部状态交出去 | `TestRecentReturnsACopy` | PASS |

Windows 上跑这些测试需要 MSYS2 的 `mingw64\bin` 在 `PATH` 里（`libopus-0.dll` 在那儿），
否则测试进程起不来，报 `0xc0000135`。

本机实测：Receiver 连上 Hub 后打开 `http://127.0.0.1:7420/monitor`，同一页上显示运行态
（Hub、输出设备、回传、中继模式、配置文件路径）、上行与回传两个电平表、RTP 计数，以及
最近日志（`/api/events`）；页面控制台无 error / warning。
