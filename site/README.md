# relaymic.com 落地页

投流测试用的英文主站：讲清楚空档、收邮箱、给隐私政策一个能读的版本。
静态页 + 一个 Worker 接口，跑在 Cloudflare Workers 上，起步零成本。

## 结构

```
public/            静态资源，整站 ~85KB（含自托管字体）
  index.html       落地页
  privacy.html     隐私政策（GDPR）
  terms.html       条款
  _headers         安全响应头与缓存策略
  assets/
    site.css       全部样式
    signal.js      首屏信号面板 + 邮箱表单
    fonts/         Archivo + IBM Plex Mono（自托管，见下）
src/index.js       Worker：/api/subscribe 之外的请求全部交给静态资源
schema.sql         waitlist 表
tools/             OG 图的渲染源，不参与部署
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

## 看名单

```sh
npm run waitlist          # 最近 50 条
npm run waitlist:count    # 按天 × 来源聚合，投流复盘看这个
```

投流链接带上 `?ref=` 就能分渠道归因，例如
`https://relaymic.com/?ref=gads-en-teamviewer`。`ref` 会跟着邮箱一起入库。

## 上线前必须处理

- [x] 数据控制者已写实：Dachun Hui，上海。同时补了第三国传输那节 ——
      控制者在中国、库在 Cloudflare 北美区，GDPR Art. 13(1)(f) 要求明说
- [x] **Cloudflare Web Analytics 自动注入已关**（`auto_install: false`）。
      详情见下节，将来新开 zone 会再踩一次
- [ ] `hey@relaymic.com` 要能真正收信（Cloudflare Email Routing 转发到常用邮箱即可，免费）
- [ ] Google Search Console 验证 + 提交 `sitemap.xml`

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

关掉的办法（Dashboard 登录态下，在浏览器控制台执行）：

```js
await fetch('/api/v4/accounts/<ACCOUNT_ID>/rum/site_info/<SITE_TAG>', {
  method: 'PUT', credentials: 'include',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ zone_tag: '<ZONE_ID>', auto_install: false }),
}).then(r => r.json())
```

改完边缘要几分钟才传播，别急着判定没生效。

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
