# RelayMic 产品仓库 · 开工指南

给在这个仓库里工作的人（和 AI 会话）。2026-08-17 由原型仓库迁移而来。

## 这个仓库是什么

**RelayMic（中文名：远麦）** —— 把任何设备的浏览器变成远程 Mac 的系统级麦克风。
商业形态：全球市场、订阅制（$4.99/月 或 $49/年，7 天试用）。

- 代码来自已验证的原型（remotemic 私有仓库）的**快照**，git 历史已重置
  （旧历史含凭证，勿从旧仓库 clone/合并历史）
- 原型仓库继续作为作者的自用生产系统运行（三台 Mac 在线），**本仓库的
  改动不回写生产**；核心 bug 修复用手动 cherry-pick 双向同步
- 全部战略上下文在 `docs/商业化.md`（定位/定价/关键词/竞品查证）和
  `docs/产品化架构.md`（服务端选型/成本/分流/本仓库的由来），**先读这两份**

## 现状

- `go build -tags nolibopusfile ./...` 已验证通过（macOS 需 `brew install opus`）
- 可用组件：Mac 接收端（WebRTC 收流+音频链+监控页）、网页发送端（多台同收+
  自动发现）、Windows CLI/GUI 发送端、诊断工具组、CI（Windows/macOS 编译）
- 测试：`go test -tags nolibopusfile ./internal/...` 全绿

## 第一批任务（按序）

### T1 改名与脱敏（半天）
1. module 改名：`go.mod` 的 `github.com/hueshu/remotemic` → `github.com/hueshu/relaymic`，
   全局更新 import，构建+测试验证
2. 清理自用硬编码（原型带过来的）：
   - `cmd/sender-gui/main.go`：默认 target 写死了作者的 IP（100.113.115.89），
     改为空+占位提示
   - `internal/web/index.html` / `cmd/receiver/main.go`：检查无凭证（应该干净，复核一遍）
   - `.github/workflows/build-sender.yml`：产物名改 relaymic
3. 品牌字符串：界面上"远程麦克风"→ RelayMic（i18n 在 T4 统一做，此处只改产品名）
4. 建 GitHub 私有仓库 `relaymic`，首次提交推送

### T2 配对码 + 信令服务（核心新工程，1~2 周）
- Cloudflare Workers + Durable Objects：6 位配对码撮合、SDP 交换、
  TURN 短时凭证签发（`use-auth-secret` 模式）
- TURN 用 Cloudflare Realtime TURN（1000GB/月免费，$0.05/GB）；
  中国区备选：作者的阿里云 coturn 可转正（多 TURN 下发，ICE 自动选路，
  见 产品化架构.md 第四节）
- 客户端接入：接收端装完显示配对码；发送端（网页为主）输码即连
- 发送端主形态是**网页**（Windows exe 冻结不做新功能，见商业化.md 已否决表）

### T3 Mac 端产品化（1~2 周）
- 菜单栏 App（状态/配对码/开关）替代 launchd 裸进程
- 签名+公证的一键 .pkg：引导安装 BlackHole（调官方 pkg，**不捆绑**，
  规避 GPL 分发）、权限引导、自动更新（Sparkle）
- 需要 Apple 开发者账号（$99/年，作者提供）

### T4 i18n（~1 周）
英文优先、中文保留：GUI、网页、监控页、全部报错话术、文档

### T5 支付与 license（~1 周）
- Paddle 或 LemonSqueezy（MoR，兜全球税务）
- license 校验 + 7 天试用；订阅状态查询走 Workers KV/D1
- 条款：个人版最多 3 台 Mac（既是条款也是中继成本护栏）

### T6 落地页
- relaymic.com（注册状态问作者）；英文主站
- 关键词与文案素材见 商业化.md 投放关键词库
- GDPR 隐私政策，亮点："音频点对点传输，不经我们服务器存储"

## 工程基线（沿袭原型的纪律）

- 注释写"为什么"，中文（用户侧文案除外）；gofmt 干净；改动带测试
- 音频链的实测参数（buffer 150ms、DTX 关、AGC 渐变门、拉伸回补）都是
  踩坑换来的，**不要"优化"它们**——背景见原型仓库 docs/手册.md 问题手册
- macOS 部署三坑已内置勿回退：say 唤醒设备、打开失败进程内重试（不退出）、
  部署先 rm 再 cp
- 部署验证用 API（curl /api/status），不用 pgrep

## 不做清单（已否决，防止绕回）

- Windows 原生 exe 新功能（网页已覆盖，exe 冻结）
- Windows→Windows 市场（RDP 官方免费）
- 手机→Mac 同网主打（Apple 连续互通免费收编）
- 免费版功能阉割/时长限制（转化靠便利差，不靠使坏）

## 问作者要的东西

- relaymic.com 域名与 Cloudflare 账户接入
- Apple 开发者账号（T3 前）
- Paddle/LemonSqueezy 账户（T5 前）
- 阿里云 coturn 凭证轮换后的新配置（若启用中国区中继）
