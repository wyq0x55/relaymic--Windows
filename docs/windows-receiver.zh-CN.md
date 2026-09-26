# Windows 接收端

> [English](windows-receiver.md) · 简体中文

RelayMic 可使用 Windows 上已安装的虚拟音频线作为麦克风桥接；它**不会**安装或模拟内核音频驱动。

## 音频路径

```
浏览器麦克风
  -> WebRTC / Opus
  -> RelayMic 接收端
  -> Windows 播放设备：CABLE Input
  -> VB-CABLE
  -> Windows 录制设备：CABLE Output
  -> Microsoft Teams 麦克风
```

`internal/audio` 通过 `malgo` 使用 miniaudio；在 Windows 上由系统选择原生音频后端。因此接收端只需把解码后的 PCM 写入 VB-CABLE 播放设备。

## 安装与启动

1. 安装 VB-CABLE（或其他 Windows 虚拟音频线）。
2. 如果驱动安装程序要求，重启电脑。
3. 在 Windows 上构建 RelayMic。
4. 配好 Hub 地址和接收端 token 后运行 `relaymic.exe`。程序会打开本机控制台；Windows 默认输出设备选择器为 `CABLE Input`。
5. 确认启动日志包含 `虚拟麦克风输出设备: CABLE Input ...`。
6. 在 Teams 中将对应的 **CABLE Output** 录制设备选为麦克风。

如果设备名里有多个 `cable`，请指定更精确的设备名：

```powershell
.\relaymic.exe receiver -device "CABLE Input"
```

连接浏览器前，可以先用项目内的 probe 测试音频线。它会列出播放设备，只向选中的虚拟线播放 440 Hz 音调；播放时应能看到 Teams 中 `CABLE Output` 有输入活动。

```powershell
go run -tags nolibopusfile ./cmd/probe -device "CABLE Input" -tone 3s
```

## 安全与失败行为

配置的输出设备不存在或匹配不唯一时，RelayMic 不会退回 Windows 默认扬声器；音频运行实例会失败，本机控制台仍可用于修改配置。但如果明确选择了物理播放设备，远端麦克风声音可能直接从真实扬声器播放。分享配对码前，请确认选中了正确的虚拟音频线。

## 本机控制台

使用 `-monitor "127.0.0.1:7420"` 启动接收端，再在同一台 Windows 电脑打开 `http://127.0.0.1:7420/monitor`。页面显示一次性配对码及倒计时、ICE 链路、实时电平、收包统计和已选设备，不会显示接收端 token。

设置页只在回环地址提供 `GET /api/devices`，扫描本机设备并分组显示为「虚拟线」和「其他设备」。重要路径是向虚拟麦克风播放，以及从第二条虚拟线采集回传音频。选择物理播放设备可能把远端声音送到真实扬声器。保存为子串（例如 `CABLE Input`）的设备名会匹配扫描出的完整名称；当前不存在的设备仍会作为当前值显示，不会被静默替换。页面打开期间安装新设备后，点「重新扫描设备」即可刷新列表。

通过局域网地址打开控制台时，配对码会隐藏；请使用上面的回环地址查看或复制配对码。

## 双向音频与公网连接

如需把 Teams 扬声器声音回传，使用第二条虚拟线：Teams 播放到 `VoiceMeeter Aux Input`，并让接收端以 `-return-device "VoiceMeeter Aux Output"` 启动。公网信令和短期 coturn 凭据见 [`signaling.md`](signaling.md)；`-force-relay` 仅用于验证 TURN 中继链路。

`relaymic`（或 `relaymic receiver`）未指定控制台参数时默认使用 `-monitor 127.0.0.1:7420 -open`，自动打开页面。`-monitor ""` 会关闭控制台；`-open=false` 只会阻止自动打开浏览器。

## 相关指南

- 交付给 Windows 用户：[`handoff.zh-CN.md`](handoff.zh-CN.md) · [English](handoff.en.md)
- 公网信令：[`signaling.md`](signaling.md) · [English](signaling.en.md)
- 部署 Hub 与 TURN：[`vps-signaling-turn-agent-guide.zh-CN.md`](vps-signaling-turn-agent-guide.zh-CN.md) · [English](vps-signaling-turn-agent-guide.en.md)
