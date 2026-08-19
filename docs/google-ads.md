# Google Ads 投放素材与执行方案

2026-08-19 整理。配套文件在 `ads/`，落地页在 `site/`（已上线 relaymic.com）。

这次投放**不是为了卖东西，是为了回答一个问题**：远程操作 Mac 的人，会不会主动搜索
「麦克风传不过去」这件事，搜到了会不会留邮箱。产品已有可用原型，钱花在验证需求上，
不是花在获客上。

判据在 `docs/商业化.md` 里事先定死了，这里只重复一次：
**落地页转化（点击 → 邮箱）> 5%，单邮箱成本 < ¥20。**

---

## 交付了什么

| 文件 | 是什么 |
| --- | --- |
| `ads/campaign.json` | 文案与关键词的**唯一真相源**。改文案改它 |
| `ads/build.py` | 从上面生成三个 CSV，同时做字符数硬校验 |
| `ads/keywords.csv` | 84 条关键词（42 个词 × 词组/完全两种匹配） |
| `ads/negatives.csv` | 55 条否定关键词 |
| `ads/ads-rsa.csv` | 5 组响应式搜索广告（每组 15 标题 + 4 描述） |
| `ads/export-conversions.py` | 把邮箱转化回传给 Google（见「转化跟踪」一节） |

### 导入步骤

1. 装 **Google Ads Editor**（免费桌面端），登录账户
2. 账户 → 导入 → 从文件导入，依次导 `keywords.csv`、`ads-rsa.csv`、`negatives.csv`
3. 检查预览里没有报错行，再点「发布」

**不要手改生成出来的 CSV。** 改 `campaign.json` 然后重跑 `python3 ads/build.py` ——
字符超限会在这一步被拦下，而 Editor 只会告诉你「某行有问题」。

字符上限是硬的：标题 30、描述 90、附加标题 25、站点链接文字 25 / 描述 35。
`build.py` 逐条按 Unicode 字符数校验，顺带拦感叹号和全大写词（Google 会拒登）。

---

## 账户结构

一个搜索广告系列，五个广告组。分组依据是**搜索者当下的心理状态**，不是产品功能 ——
同一组里的词意图越接近，质量得分越高、CPC 越低。

### Remote Mac Core　`?ref=core`

已经在找「怎么把麦克风送到远程 Mac」的人，意图最硬

关键词 8 个（每个都投词组 + 完全两种匹配，共 16 条）：

```
forward microphone remote desktop mac
how to use microphone remote desktop mac
microphone passthrough remote desktop
microphone passthrough remote desktop mac
remote desktop microphone mac
remote mac microphone
send microphone to remote mac
use microphone over remote desktop mac
```

### Remote Support Tools　`?ref=support-tools`

远程支持类工具（企业向）的用户，发现自己的麦克风传不过去

关键词 7 个（每个都投词组 + 完全两种匹配，共 14 条）：

```
anydesk microphone mac
anydesk microphone not working mac
remote support microphone mac
teamviewer microphone mac
teamviewer microphone remote mac
teamviewer microphone transmission
use microphone anydesk mac
```

### Dev Streaming Tools　`?ref=streaming-tools`

低延迟串流/开发者向远程工具的用户，多半在用云 Mac 或家里的 Mac mini

关键词 7 个（每个都投词组 + 完全两种匹配，共 14 条）：

```
cloud mac microphone
jump desktop microphone
mac screen sharing microphone
parsec mic passthrough mac
parsec microphone mac
rustdesk microphone
rustdesk microphone mac
```

### Voice Tasks　`?ref=voice-tasks`

按任务搜的人：要在远程 Mac 上口述、开会、录音

关键词 8 个（每个都投词组 + 完全两种匹配，共 16 条）：

```
browser microphone to mac
dictation on remote mac
record on remote mac
remote mac dictation microphone
use browser as microphone mac
use phone as microphone for remote mac
virtual microphone mac remote
voice input remote mac
```

### Espanol Mac Remoto　`?ref=es`

西语市场：AnyDesk/TeamViewer 占有率高，痛点表述和英文不同

关键词 12 个（每个都投词组 + 完全两种匹配，共 24 条）：

```
anydesk micrófono no funciona
anydesk micrófono no funciona mac
dictado por voz mac remoto
micrófono escritorio remoto mac
micrófono remoto mac
micrófono virtual mac
pasar micrófono por escritorio remoto
teamviewer micrófono mac
usar celular como micrófono para mac
usar micrófono en escritorio remoto
usar micrófono en escritorio remoto mac
usar móvil como micrófono mac
```
---

## 为什么广告文案里一个竞品名都没有

关键词里有 `teamviewer microphone remote mac`、`anydesk micrófono no funciona` 这类词，
**但文案里不会出现任何竞品商标**。这是有意为之。

Google Ads 的商标政策：商标**可以用作关键词**竞价（多数地区自 2004 年起就允许），
但**不能出现在广告文案中**，除非广告主是该产品的转销商、或是提供竞品信息的资讯站点。
RelayMic 两者都不是。写「Mic Missing in AnyDesk」这种文案，商标方一投诉就会被下架，
而且是整条广告被禁，不是改一改的事。

所以竞品组的文案走的是**通用表述**：

> Mic Missing in Your Session / Keep Your Tool, Add a Mic /
> Your remote tool moves screen and keyboard. RelayMic moves your voice to the Mac.

搜索者心里已经装着那个产品名了，广告不必替他念一遍 —— 他要的是「有解」这个信号。

如果你想赌一把商标文案（转化通常更高），做法是：单开一个广告组小额测试，
被拒登就撤，不要在主力组里试。

---

## 转化跟踪：和"零第三方脚本"的冲突，以及怎么绕开

Google Ads 的标准转化跟踪要在页面上装 gtag.js。**但落地页的 CSP 是
`script-src 'self'`，隐私政策白纸黑字写着"no analytics scripts / no third-party tags"**
（那一条刚为了 Cloudflare 的 beacon 清理过一轮）。装 gtag 等于自食其言，而且
CSP 会直接把它拦下 —— 重演一遍 beacon 那出戏。

三条路，选第三条：

| 方案 | 代价 |
| --- | --- |
| 装 gtag | 破坏零第三方承诺，隐私政策要改口径，CSP 要放行 |
| 什么都不装，只用 `?ref=` | 我们自己能算转化率，但 Google 拿不到转化信号，智能出价用不了 |
| **捕获 `gclid` + 离线转化导入** | 页面零脚本；Google 照样拿到转化数据 |

**gclid 是 Google 自己在落地页 URL 上加的点击 ID**（自动标记开启时每次点击都带）。
把它连同邮箱一起存进 D1，事后按 Google Ads 的离线转化导入格式回传，Google 就能把
"这个点击最终留了邮箱"对上号 —— 全程不需要客户端跑任何第三方代码。

代价是转化数据不是实时的，要手动（或定时）回传。投放期两周、总预算 ¥1000 的规模，
手动回传完全够用。

**这件事必须在开投前做完**：没捕获的 gclid 是永久丢失的，事后补不回来。

## 预算与出价

总预算 ¥1000（≈$140），两周，日预算 $10 封顶。这是**通过/不通过测试**，不是
放量campaign —— 目标是拿到判断依据，不是拿到用户。

- **出价策略：手动 CPC 起步**，不要一上来就智能出价。新账户没有转化历史，
  智能出价会拿你的预算去学习，而两周 ¥1000 学不出东西
- 首次出价上限 $1.50。niche 词的竞争通常不激烈，实际 CPC 大概率低得多
- **只投搜索网络**。展示网络、搜索伙伴、Performance Max 全部关掉 ——
  它们会把预算花在"看起来像但不是"的流量上，niche 品类尤其严重
- 地区：先投 美国 / 加拿大 / 英国 / 澳大利亚 / 德国 / 荷兰（英文组），
  西班牙 / 墨西哥 / 阿根廷（西语组）。避开印度、东南亚 —— 不是这个价位的客群
- 语言按广告组的语言设，别混

## 归因：?ref= 与 gclid 并存

每个广告组的最终 URL 带自己的 `?ref=`（已在 CSV 里配好），邮箱入库时一起存。
这样不依赖任何外部平台就能算出「哪个广告组带来了几个邮箱」。

gclid 由 Google 自动加在 URL 后面，和 ref 并存，互不冲突。

## 开投前检查清单

- [ ] 落地页捕获并存储 `gclid`（见上，**必须先做**）
- [ ] Google Ads 账户开启自动标记（auto-tagging），否则没有 gclid
- [ ] 关掉搜索伙伴、展示网络扩展
- [ ] 日预算设 $10，且在账户层面设一个总额提醒
- [ ] 每个广告组的最终 URL 带对了自己的 `?ref=`
- [ ] 用 Google Ads 的广告预览工具确认广告真的出得来（别用自己搜，会烧展示次数还不准）
- [ ] 落地页在手机上打开正常（一半以上的搜索流量来自手机）

## 每天看什么

一天看一次就够，不要一小时刷一次 —— 数据量小的时候，短时波动全是噪声。

```sh
cd site && npm run waitlist    # 邮箱数、按来源拆分
```

对照 Google Ads 后台的点击数，算：**转化率 = 邮箱数 ÷ 点击数**。

三个数字决定去留：
1. **转化率 > 5%** —— 这是过线标准里最硬的一条
2. **单邮箱成本 < ¥20**
3. **有没有搜索量** —— 如果两周下来展示次数少得可怜，说明这个需求小到广告触达不了，
   这本身就是答案（而且是省钱的答案）

## 什么时候停

- 预算烧完就停。**"再加点预算试试"是沉没成本在说话** —— 这条纪律是事先定死的，
  不是烧到一半再商量的
- 如果第一周转化率明显低于 5%（比如 1-2%），不必等第二周。先改落地页文案再说，
  或者直接判定不过线
- 如果转化率高但成本也高，那是好消息：需求真实，只是 CPC 贵，可以调关键词结构

## 常见坑

- **别投宽泛匹配**。这个品类的宽泛匹配会把预算喂给"mac microphone not working"
  这类修硬件的人。全部用词组和完全匹配起步
- **竞品词要谨慎写文案**。可以说"works alongside TeamViewer"，不能说
  "better than TeamViewer" —— Google 对贬低竞品的文案会拒登，商标词还可能被投诉
- **别在广告里承诺落地页没写的东西**。着陆页体验是质量得分的一部分，
  文案和页面对不上，CPC 会被罚高

---

## 这批素材是怎么来的

两个模型拿同一份 brief 各写一版，我做合并：

- **codex**（gpt-5.6-sol，xhigh）：5 个广告组、62 关键词、75 标题。分组更细，
  文案用 Title Case，竞品组用「共存」定位而非贬低 —— 这个判断是对的
- **grok**：4 个广告组、42 关键词。否定关键词库明显更强（50+ 条，中英西三语，
  覆盖免费/破解、招聘、Continuity、Windows RDP、硬件维修、买麦克风），
  站点链接和附加标题也齐全。有一条描述很好：主动写明「不是给同屋 iPhone 用的」，
  等于在广告位上先筛掉一批错的人

两边的字符数**都是零超长**（codex 105 条、grok 86 条全部合规）—— 这一点超出预期，
但仍然值得程序化复核，因为超一个字符整行导入就失败。

合并取舍：采用 codex 的分组与 Title Case 文案，grok 的否定词库与扩展，
两边的关键词并集去重。**竞品商标从所有文案中移除** —— 这是两边都犯的错。

