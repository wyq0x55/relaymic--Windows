// relaymic.com 的 Worker：静态站点 + 一个收邮箱的接口。
//
// 页面本身全是静态资源，由 env.ASSETS 直接吐出。这里只拦一条路径，
// 因为投流阶段唯一需要服务端的动作就是把邮箱写进名单。

import { EmailMessage } from 'cloudflare:email';

const EMAIL = /^[^\s@,;:<>()[\]\\]+@[^\s@.,;:<>()[\]\\]+(\.[^\s@.,;:<>()[\]\\]+)+$/;

// 通知的收发地址。收件人在 wrangler.jsonc 的 binding 上也锁了一份 ——
// 那层锁是硬的，代码写错也发不出去别处。
const NOTIFY_FROM = 'notify@relaymic.com';
const NOTIFY_TO = 'chshu4@gmail.com';

export default {
  async fetch(request, env, ctx) {
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
      return subscribe(request, env, ctx);
    }

    return env.ASSETS.fetch(request);
  },
};

async function subscribe(request, env, ctx) {
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

    // 只有真的新增才通知 —— 重复提交没有信息量，攒多了就没人看通知了。
    // 发信挂在 waitUntil 上：投流期间响应速度直接影响转化，
    // 不能让访客等一封给我自己看的邮件。发失败也只记日志，
    // 名单已经落库了，通知丢了不影响任何事。
    if (!already && env.NOTIFY) {
      ctx.waitUntil(
        notify(env, { email, country, ref }).catch((err) => {
          console.error('通知邮件发送失败', err);
        })
      );
    }

    return json({ ok: true, already });
  } catch (err) {
    console.error('waitlist insert failed', err);
    return json({ error: "We couldn't save that just now. Try again in a moment." }, 500);
  }
}

async function notify(env, { email, country, ref }) {
  // 总数放在通知里，省得为了看"现在多少条了"再去查一次库
  const row = await env.DB.prepare('SELECT count(*) AS n FROM waitlist').first();
  const total = row?.n ?? '?';

  const subject = `RelayMic 名单 +1（共 ${total}）· ${email}`;
  const text = [
    `邮箱：${email}`,
    `国家：${country || '未知'}`,
    `来源：${ref || '（直接访问，无 ref 参数）'}`,
    `当前总数：${total}`,
    '',
    `时间：${new Date().toISOString()}`,
    '',
    '看完整名单：cd site && npm run waitlist',
  ].join('\n');

  await env.NOTIFY.send(new EmailMessage(NOTIFY_FROM, NOTIFY_TO, mime(subject, text)));
}

// 手写 MIME。发的是一封纯文本短信件，为它引一个库不值得。
// 主题和正文都走 base64：中文在邮件头里必须按 RFC 2047 编码，
// 正文不编码则依赖各家客户端对 8bit 的容忍度，不如直接编死。
function mime(subject, text) {
  const id = crypto.randomUUID();
  return [
    `From: RelayMic <${NOTIFY_FROM}>`,
    `To: ${NOTIFY_TO}`,
    `Subject: =?UTF-8?B?${b64(subject)}?=`,
    `Message-ID: <${id}@relaymic.com>`,
    `Date: ${new Date().toUTCString()}`,
    'MIME-Version: 1.0',
    'Content-Type: text/plain; charset=utf-8',
    'Content-Transfer-Encoding: base64',
    '',
    // RFC 2045 要求 base64 正文每行不超过 76 字符
    b64(text).replace(/(.{76})/g, '$1\r\n'),
  ].join('\r\n');
}

function b64(s) {
  const bytes = new TextEncoder().encode(s);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
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
