#!/usr/bin/env python3
"""导出 Google Ads 离线转化导入用的 CSV。

页面上不装 gtag（CSP 是 script-src 'self'，隐私政策也写着无第三方脚本），
所以 Google 那边默认看不到任何转化。补法是把落地时捕获的 gclid 连同转化时间
回传给它 —— 上传后 Google Ads 就能把「哪次点击最终留了邮箱」对上号。

用法（在仓库根目录）：
    python3 ads/export-conversions.py > conversions.csv

然后在 Google Ads 后台：工具与设置 → 转化 → 导入 → 上传文件。
上传前要先建一个名为 CONVERSION_NAME 的转化动作，来源选「导入」。
"""
import csv, json, os, subprocess, sys

CONVERSION_NAME = "Waitlist signup"   # 必须和后台里建的转化动作同名，差一个字都对不上
VALUE = "1"                            # 一个邮箱记 1，投放期不做价值加权
CURRENCY = "USD"

HERE = os.path.dirname(os.path.abspath(__file__))
SITE = os.path.join(os.path.dirname(HERE), "site")   # wrangler 要在 site/ 里跑


def query(sql):
    r = subprocess.run(
        ["npx", "wrangler", "d1", "execute", "relaymic", "--remote", "--json", "--command", sql],
        capture_output=True, text=True, cwd=SITE,
    )
    if r.returncode != 0:
        sys.exit(f"查询失败：\n{r.stderr}")
    return json.loads(r.stdout)[0]["results"]


rows = query(
    "SELECT gclid, created_at FROM waitlist "
    "WHERE gclid IS NOT NULL AND gclid != '' ORDER BY id"
)
if not rows:
    sys.exit("没有带 gclid 的记录 —— 要么还没开投，要么账户没开自动标记（auto-tagging）。")

w = csv.writer(sys.stdout)
# 首行是 Google 要求的参数行。created_at 存的是 UTC，所以时区写 +0000，
# 别改成本地时区 —— 时间对不上会让转化归因到错误的点击。
w.writerow(["Parameters:TimeZone=+0000"])
w.writerow(["Google Click ID", "Conversion Name", "Conversion Time",
            "Conversion Value", "Conversion Currency"])
for r in rows:
    w.writerow([r["gclid"], CONVERSION_NAME, r["created_at"], VALUE, CURRENCY])

print(f"共 {len(rows)} 条", file=sys.stderr)
