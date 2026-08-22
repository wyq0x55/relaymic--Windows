// relaymic.com 的 Worker。
//
// 站点是纯静态的，这里只做一件服务端才能做的事：把 www 收敛到根域。
// 其余请求原样交给静态资源。
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

    return env.ASSETS.fetch(request);
  },
};
