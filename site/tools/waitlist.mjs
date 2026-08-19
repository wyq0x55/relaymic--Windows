#!/usr/bin/env node
// 看名单。投流期间这是每天要看的东西，所以它得一眼能读 ——
// wrangler 原样吐出来的是带 served_by_colo、sql_duration_ms 的完整 JSON，
// 每次还要自己在里面找那几行数据。
//
// 用法：npm run waitlist         最近 20 条
//       npm run waitlist -- --all  全部

import { execFileSync } from 'node:child_process';

const ALL = process.argv.includes('--all');

function query(sql) {
  const out = execFileSync(
    'npx',
    ['wrangler', 'd1', 'execute', 'relaymic', '--remote', '--json', '--command', sql],
    { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] },
  );
  return JSON.parse(out)[0].results;
}

// 中文按两格宽算，不然表格会错位
const w = (s) => [...String(s ?? '')].reduce((n, c) => n + (/[⺀-鿿＀-￯]/.test(c) ? 2 : 1), 0);
const pad = (s, n) => String(s ?? '') + ' '.repeat(Math.max(0, n - w(s)));

const [{ total }] = query('SELECT count(*) AS total FROM waitlist');

if (total === 0) {
  console.log('\n名单还是空的。\n');
  process.exit(0);
}

// 今天/昨天用 UTC 算，和 created_at 的存储口径一致
const days = query(`
  SELECT date(created_at) AS day, count(*) AS n
  FROM waitlist GROUP BY day ORDER BY day DESC LIMIT 7
`);
const today = days.find((d) => d.day === new Date().toISOString().slice(0, 10))?.n ?? 0;

console.log(`\n名单 ${total} 条 · 今日 +${today}`);

console.log('\n按天（UTC）');
for (const d of days) console.log(`  ${d.day}  ${String(d.n).padStart(4)}`);

const refs = query(`
  SELECT coalesce(nullif(ref, ''), '(直接访问)') AS ref, count(*) AS n
  FROM waitlist GROUP BY ref ORDER BY n DESC
`);
console.log('\n按来源');
const refW = Math.max(...refs.map((r) => w(r.ref)), 8);
for (const r of refs) console.log(`  ${pad(r.ref, refW)}  ${String(r.n).padStart(4)}`);

const rows = query(`
  SELECT created_at, email, coalesce(country, '-') AS country,
         coalesce(nullif(ref, ''), '-') AS ref
  FROM waitlist ORDER BY created_at DESC, id DESC LIMIT ${ALL ? 1000 : 20}
`);
console.log(`\n明细（最近 ${rows.length} 条${ALL ? '' : '，全部用 npm run waitlist -- --all'}）`);
const emailW = Math.max(...rows.map((r) => w(r.email)), 5);
const refW2 = Math.max(...rows.map((r) => w(r.ref)), 4);
console.log(`  ${pad('时间(UTC)', 19)}  ${pad('邮箱', emailW)}  ${pad('国家', 4)}  ${pad('来源', refW2)}`);
for (const r of rows) {
  console.log(`  ${pad(r.created_at, 19)}  ${pad(r.email, emailW)}  ${pad(r.country, 4)}  ${pad(r.ref, refW2)}`);
}
console.log('');
