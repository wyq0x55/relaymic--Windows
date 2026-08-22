// relaymic.com 的 Worker。
//
// 站点是纯静态的，这里只做两件必须在服务端做的事：把 www 收敛到根域，
// 以及给中国访客一次去中文版的机会。
//
// 曾经这里还有一个 /api/subscribe 收邮箱、写 D1、发通知邮件。项目转为
// 纯开源后那条链路整个拆掉了 —— 没有产品要卖，就没有名单要收；而少收
// 一样东西，隐私政策里就能少写一条。对一个处理用户声音的工具来说，
// 「我们不收集任何数据」比任何承诺都有说服力，前提是它得是真的。

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // 两个域名都能打开等于把外链和搜索权重劈成两半，canonical 也指的是根域。
    if (url.hostname === 'www.relaymic.com') {
      url.hostname = 'relaymic.com';
      return Response.redirect(url.toString(), 301);
    }

    // 中国访客首次落到根域时送去中文版。
    //
    // 三个刻意的限制，都是为了别把跳转做成牢笼：
    //   1. 只认根路径 —— 深链接（/privacy 之类）原样打开，不劫持
    //   2. 带站内 Referer 就不跳 —— 用户在中文页点了 "English" 回来，
    //      是明确表达过意愿的，再跳一次等于不让他看英文版
    //   3. 用 302 不用 301 —— 这是按地区分流，不是永久搬家，
    //      而且 301 会被浏览器缓存死，用户换语言就更难了
    //
    // 不用 Cookie 记住选择：本站承诺不放 Cookie，那条承诺比这点便利值钱。
    const country = request.cf?.country;
    const ref = request.headers.get('Referer') || '';
    if (country === 'CN' && url.pathname === '/' && !ref.includes('relaymic.com')) {
      return Response.redirect(new URL('/zh/', url).toString(), 302);
    }

    return env.ASSETS.fetch(request);
  },
};
