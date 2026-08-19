-- 投流阶段的全部服务端状态就这一张表。
-- email 上的 UNIQUE 让重复提交变成幂等操作，不需要在代码里先查后写。
CREATE TABLE IF NOT EXISTS waitlist (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  email      TEXT NOT NULL UNIQUE,
  country    TEXT,
  ref        TEXT,
  -- Google 自动标记加在落地页 URL 上的点击 ID。存它是为了事后做离线转化导入 ——
  -- 页面上不装 gtag（CSP 是 script-src 'self'，隐私政策也写着无第三方脚本），
  -- 靠回传 gclid 让 Google Ads 拿到转化信号。
  gclid      TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_waitlist_created ON waitlist (created_at);
CREATE INDEX IF NOT EXISTS idx_waitlist_ref ON waitlist (ref);
