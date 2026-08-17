-- 投流阶段的全部服务端状态就这一张表。
-- email 上的 UNIQUE 让重复提交变成幂等操作，不需要在代码里先查后写。
CREATE TABLE IF NOT EXISTS waitlist (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  email      TEXT NOT NULL UNIQUE,
  country    TEXT,
  ref        TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_waitlist_created ON waitlist (created_at);
CREATE INDEX IF NOT EXISTS idx_waitlist_ref ON waitlist (ref);
