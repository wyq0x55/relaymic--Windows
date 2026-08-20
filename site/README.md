# relaymic.com 落地页

投流测试用的英文主站：讲清楚空档、收邮箱、给隐私政策一个能读的版本。
静态页 + 一个 Worker 接口，跑在 Cloudflare Workers 上，起步零成本。

## 结构

```
public/            静态资源，整站 ~85KB（含自托管字体）
  index.html       落地页
  privacy.html     隐私政策（GDPR）
  terms.html       条款
  es/              西语版三页，共用同一套 CSS/JS
  _headers         安全响应头与缓存策略
  assets/
    site.css       全部样式
    signal.js      首屏信号面板 + 邮箱表单
    fonts/         Archivo + IBM Plex Mono（自托管，见下）
src/index.js       Worker：/api/subscribe 之外的请求全部交给静态资源
schema.sql         waitlist 表
tools/waitlist.mjs 看名单的脚本
tools/og-*         OG 图的渲染源，不参与部署
```

**字体自托管不是洁癖**：引用 Google Fonts CDN 会把访客 IP 送给第三方，欧盟已有判例。
`_headers` 里的 CSP 也因此能收得很紧 —— 页面不加载任何外部域。

## 本地开发

```sh
npm install
npm run db:init:local     # 建本地 D1 表
npm run dev               # http://localhost:8788
```

改完 CSS/JS 直接刷新即可；`_headers` 给 `/assets/*` 设了一小时缓存，
本地验证时记得强制刷新（⌘⇧R）。

## 部署

**已上线**：https://relaymic.com （2026-08-17）。Worker 名 `relaymic-site`，
D1 库 `relaymic`（id 已填在 `wrangler.jsonc`），自定义域在 `routes` 里声明，
`wrangler deploy` 会一并接好，不用去控制台点。

日常发版只要：

```sh
npm run deploy
```

`www.relaymic.com` 也绑了，但由 Worker 301 到根域 —— 两个域都能打开会把外链
和搜索权重劈成两半，而 canonical 指的是根域。

从零重建（换账户之类）时补两步：`npx wrangler d1 create relaymic` 拿到
database_id 填进 `wrangler.jsonc`，再 `npm run db:init` 建表。

## 西语版

`/es/`、`/es/privacy`、`/es/terms`。存在的理由只有一个：西语广告组点进来不能落到英文页 ——
着陆页语言和广告语言对不上，转化率和质量得分一起挨罚，而质量得分是**整个广告系列**
的 CPC，不只是那一组。

三页共用 `site.css` 和 `signal.js`，DOM 结构与英文版逐节对应，改版式时两边一起改。
西语文本比英文长 15–25%，改动后值得在 390px 宽下看一眼表格和 H1。

表单提示按 `<html lang>` 选文案，表在 `signal.js` 的 `COPY` 里。**服务端不返回人话**，
出错只返回 `code`（`bad_email` 之类），页面自己查表 —— 否则同一句文案要在
Worker 和前端各维护一份，改一次得记得改两处。

`hreflang` 三页互指，`sitemap.xml` 里六条 URL 都在。

## 看名单

```sh
npm run waitlist            # 总数、今日新增、按天、按来源、最近 20 条明细
npm run waitlist -- --all   # 明细不截断
```

投流链接带上 `?ref=` 就能分渠道归因，例如
`https://relaymic.com/?ref=gads-en-teamviewer`。`ref` 会跟着邮箱一起入库。

## 上线前必须处理

- [x] 数据控制者已写实：Shu Chunhui，上海。同时补了第三国传输那节 ——
      控制者在中国、库在 Cloudflare 北美区，GDPR Art. 13(1)(f) 要求明说
- [x] **Cloudflare Web Analytics 自动注入已关**（`auto_install: false`）。
      详情见下节，将来新开 zone 会再踩一次
- [x] `hey@relaymic.com` 已能收信：Email Routing 已启用，规则 `hey@ → chshu4@gmail.com`
- [x] 新邮箱进名单会发通知邮件到 chshu4@gmail.com，见下节
- [x] 落地页捕获 `gclid` 并入库 —— 投放的转化跟踪靠它，见 `docs/google-ads.md`
- [x] 西语落地页 `/es/` 已上线，西语广告组指过去（见下节）
- [x] Google Search Console 已验证（Domain 属性 `sc-domain:relaymic.com`），
      `sitemap.xml` 已提交，Google 读到 6 条 URL（英西各三页）。
      验证方式是 Domain Connect —— Google 经授权往 Cloudflare 写了一条
      `google-site-verification=` 的 TXT。想收回授权就去 Cloudflare 那边断开连接，
      TXT 留着，验证不受影响

## 新邮箱通知

有人提交邮箱、且是新地址时，Worker 发一封通知到 `chshu4@gmail.com`：主题带当前总数，
正文是邮箱、国家、来源。重复提交不通知 —— 没有信息量，攒多了就没人看了。

几个刻意的选择：

- **发信挂在 `ctx.waitUntil()` 上**。投流期间响应速度直接影响转化，不能让访客
  等一封给作者自己看的邮件。发失败只记日志，名单已经落库，通知丢了不影响任何事
- **`destination_address` 锁在 binding 上**（`wrangler.jsonc`）。这层限制在平台侧，
  代码写错也发不到别处去
- **MIME 是手写的**。一封纯文本短信件，为它引一个库不值得。主题和正文都走 base64：
  中文在邮件头里必须按 RFC 2047 编码，正文不编码则要赌各家客户端对 8bit 的容忍度

发信域名 `relaymic.com` 的 MX / SPF / DKIM 由 Email Routing 启用时自动配好，
实测邮件进 Gmail 收件箱，不是垃圾箱。

改收件人要动三处：`wrangler.jsonc` 的 `destination_address`、`src/index.js` 的
`NOTIFY_TO`、以及 Cloudflare 那边的目标地址验证（新地址要先在 Email Routing 里验证过）。

## 那个自动注入的 beacon（记下来，免得下次再查一遍）

Cloudflare 会给**每个新建的代理 zone** 自动建一个 Web Analytics 站点并开
`auto_install`，往 HTML 里塞 `static.cloudflareinsights.com` 的 beacon。
本站 CSP 是 `script-src 'self'`，于是它被拦下 —— 数据一条收不到，每个访客还白发
一个被拒请求、console 留一条红色报错。

排查时有三个坑：

1. **纯 curl 看不见它**。注入只对"像浏览器"的请求做，要带上 UA 和 `Accept: text/html`
   才复现得出来
2. **zone 页面里的「真实用户度量 (RUM)」显示未启用**，看着像没开，但注入照旧 ——
   那个开关和这个 auto_install 不是一回事
3. **beacon 里的 `token` 不是 site_tag**。按 token 拼管理页 URL 打不开，
   要用 `site_token` 去 `rum/site_info/list` 里反查对应的 `site_tag`

**结论：别指望从 Cloudflare 那边关掉它。** 两条配置都试过，都确认落库、没被回滚，
边缘等了 12 分钟照样注入：

```js
// 1. auto_install —— 这只是「新 zone 要不要自动装」的标记，
//    关掉它不会撤下已经生效的注入规则
await fetch('/api/v4/accounts/<ACCT>/rum/site_info/<SITE_TAG>', {
  method: 'PUT', credentials: 'include',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ zone_tag: '<ZONE>', auto_install: false }),
}).then(r => r.json())

// 2. ruleset 里那条 host:*/paths:* 的规则 —— 真正在注入的就是它。
//    注意路由：读用复数 /rules，写用单数 /rule/{id}，写成复数一律 404
await fetch('/api/v4/accounts/<ACCT>/rum/v2/<RULESET_ID>/rule/<RULE_ID>', {
  method: 'PUT', credentials: 'include',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ host: '*', paths: ['*'], inclusive: true, is_paused: true }),
}).then(r => r.json())
```

**真正解决它的是 `_headers` 里给 HTML 加的 `no-transform`**（见该文件的注释）。
标准指令，禁止中间代理改动响应体，Cloudflare 据此关掉对 HTML 的全部后处理。
实测 brotli 压缩不受影响。

加完还要**清一次 zone 缓存**，否则边缘上那份加 no-transform 之前的旧 HTML 还在，
带随机查询参数也照样命中：

```js
await fetch('/api/v4/zones/<ZONE>/purge_cache', {
  method: 'POST', credentials: 'include',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ purge_everything: true }),
}).then(r => r.json())
```

真要装分析，得同时改 CSP 放行和隐私政策里"无分析脚本"那段 —— 两者必须同步，
否则政策就是假的。

## 重新生成 OG 图

`tools/` 里是渲染源。改完文案后：

```sh
cp tools/og-source.html public/ && cp tools/og.css tools/og.js public/assets/
npm run dev
# 浏览器视口设成 1200×630 打开 /og-source.html，截图存为 public/assets/og.png
sips -Z 1200 public/assets/og.png --out public/assets/og.png
rm public/og-source.html public/assets/og.css public/assets/og.js
```

## 过线标准（来自 docs/商业化.md，先定死免得事后靠感觉）

- 落地页转化（点击 → 邮箱）> 5%
- 单邮箱成本 < ¥20
- 预售 ≥ 5 单 —— 有这条其他都可忽略（当前页面收的是邮箱，不是预售，
  真要测支付意愿得把 CTA 换成付款页）

预算封顶就是封顶。数据不过线时"再加点预算试试"是沉没成本在说话。
