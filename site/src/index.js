// relaymic.com 的 Worker：静态站点 + 一个收邮箱的接口。
//
// 页面本身全是静态资源，由 env.ASSETS 直接吐出。这里只拦一条路径，
// 因为投流阶段唯一需要服务端的动作就是把邮箱写进名单。

const EMAIL = /^[^\s@,;:<>()[\]\\]+@[^\s@.,;:<>()[\]\\]+(\.[^\s@.,;:<>()[\]\\]+)+$/;

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    // 两个域名都能打开等于把外链和搜索权重劈成两半，canonical 也指的是根域。
    if (url.hostname === 'www.relaymic.com') {
      url.hostname = 'relaymic.com';
      return Response.redirect(url.toString(), 301);
    }

    if (url.pathname === '/api/subscribe') {
      if (request.method !== 'POST') {
        return json({ error: 'Method not allowed' }, 405);
      }
      return subscribe(request, env);
    }

    return env.ASSETS.fetch(request);
  },
};

async function subscribe(request, env) {
  let body;
  try {
    body = await request.json();
  } catch {
    return json({ error: "That didn't come through. Try again." }, 400);
  }

  // 蜜罐：真人看不见这个字段，脚本会填。填了就假装成功，不落库、不给反馈。
  if (typeof body.company === 'string' && body.company.trim() !== '') {
    return json({ ok: true });
  }

  const email = String(body.email || '').trim().toLowerCase();
  if (email.length > 254 || !EMAIL.test(email)) {
    return json({ error: 'That address looks off — check it and try again.' }, 400);
  }

  // 只留投流复盘真正要用的三样：地址、来源、国家。
  // 不存 IP，也不存 UA —— 存了就得在隐私政策里交代，而它们对决策没用。
  const country = request.cf?.country ?? null;
  const ref = String(body.ref || '').slice(0, 64) || null;

  try {
    const res = await env.DB.prepare(
      'INSERT OR IGNORE INTO waitlist (email, country, ref) VALUES (?, ?, ?)'
    )
      .bind(email, country, ref)
      .run();

    // changes === 0 说明这个地址已经在名单里了。这不是错误，
    // 但用户值得知道自己没白填第二遍。
    const already = res.meta.changes === 0;
    return json({ ok: true, already });
  } catch (err) {
    console.error('waitlist insert failed', err);
    return json({ error: "We couldn't save that just now. Try again in a moment." }, 500);
  }
}

function json(data, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Cache-Control': 'no-store',
    },
  });
}
