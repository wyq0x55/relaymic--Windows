#!/usr/bin/env python3
"""从 campaign.json 生成 Google Ads Editor 能直接导入的 CSV。

campaign.json 是文案的唯一真相源 —— 改文案改它，然后重跑这个脚本，
不要手改生成出来的 CSV。

字符上限是硬的：超一个字符，Editor 导入时整行失败，而且它的报错
只会告诉你"某行有问题"。所以这里先校验、不合格就拒绝生成。
"""
import csv, json, sys, pathlib

HERE = pathlib.Path(__file__).parent
CAMPAIGN = "RelayMic - Search - Test"   # 用 ASCII 连字符：en dash 在后台里不好输入，也容易在转码时出岔

# 批量上传靠数字 ID 定位已存在的广告系列 —— 只给名字会整表报
# "Missing value in Campaign ID"。这个 ID 在广告系列创建后从后台 URL 里取。
CAMPAIGN_ID = "24156845915"

# Google Ads 的硬上限（按 Unicode 字符数，不是字节）
LIMITS = {"headline": 30, "description": 90, "callout": 25, "sitelink_text": 25, "sitelink_desc": 35}


def check(data):
    """把所有超长和违规项收集齐再一次报出来，别改一条跑一次。"""
    bad = []

    def over(kind, text, where):
        n = len(text)
        if n > LIMITS[kind]:
            bad.append(f"{where}: {kind} 超 {n - LIMITS[kind]} 字符（{n}/{LIMITS[kind]}）— {text!r}")

    for g in data["ad_groups"]:
        w = g["name"]
        if len(g["headlines"]) < 3:
            bad.append(f"{w}: 标题少于 3 条，RSA 至少要 3 条")
        if not g["descriptions"]:
            bad.append(f"{w}: 没有描述")
        for h in g["headlines"]:
            over("headline", h, w)
            # Google 对标题里的感叹号有限制，全大写词会被拒
            if "!" in h:
                bad.append(f"{w}: 标题带感叹号 — {h!r}")
            for word in h.split():
                if len(word) > 2 and word.isupper() and word.isalpha():
                    bad.append(f"{w}: 标题有全大写词 {word!r} — {h!r}")
        for d in g["descriptions"]:
            over("description", d, w)
        if len(set(g["headlines"])) != len(g["headlines"]):
            bad.append(f"{w}: 标题有重复")

    for c in data.get("callouts", []):
        over("callout", c, "callout")
    for s in data.get("sitelinks", []):
        over("sitelink_text", s["text"], "sitelink")
        over("sitelink_desc", s.get("desc1", ""), "sitelink")
        over("sitelink_desc", s.get("desc2", ""), "sitelink")
    return bad


def write(name, header, rows):
    p = HERE / name
    with p.open("w", newline="", encoding="utf-8-sig") as f:
        w = csv.writer(f)
        w.writerow(header)
        w.writerows(rows)
    print(f"  {name}  {len(rows)} 行")


def main():
    data = json.loads((HERE / "campaign.json").read_text(encoding="utf-8"))

    bad = check(data)
    if bad:
        print(f"校验不通过，{len(bad)} 处问题：\n")
        for b in bad:
            print(" ", b)
        sys.exit(1)

    # 关键词
    rows = []
    for g in data["ad_groups"]:
        for k in g["keywords"]:
            rows.append([CAMPAIGN_ID, CAMPAIGN, g["name"], k["text"], k["match"].capitalize()])
    write("keywords.csv", ["Campaign ID", "Campaign", "Ad Group", "Keyword", "Criterion Type"], rows)

    # 否定关键词：广告系列级 + 广告组级
    rows = [[CAMPAIGN_ID, CAMPAIGN, "", n, "Campaign Negative Phrase"] for n in data.get("campaign_negatives", [])]
    for g in data["ad_groups"]:
        rows += [[CAMPAIGN_ID, CAMPAIGN, g["name"], n, "Negative Phrase"] for n in g.get("negatives", [])]
    write("negatives.csv", ["Campaign ID", "Campaign", "Ad Group", "Keyword", "Criterion Type"], rows)

    # 响应式搜索广告：每组一条，标题/描述各占一列
    header = (["Campaign ID", "Campaign", "Ad Group", "Ad type"]
              + [f"Headline {i}" for i in range(1, 16)]
              + [f"Description {i}" for i in range(1, 5)]
              + ["Final URL", "Path 1", "Path 2"])
    rows = []
    for g in data["ad_groups"]:
        hs = (g["headlines"] + [""] * 15)[:15]
        ds = (g["descriptions"] + [""] * 4)[:4]
        # 西语组落到西语页 —— 西语广告点进英文页会同时砸转化率和质量得分
        url = g.get("final_url") or f"https://relaymic.com/?ref={g['final_url_ref']}"
        rows.append([CAMPAIGN_ID, CAMPAIGN, g["name"], "Responsive search ad"] + hs + ds
                    + [url, g.get("path1", ""), g.get("path2", "")])
    write("ads-rsa.csv", header, rows)

    n_kw = sum(len(g["keywords"]) for g in data["ad_groups"])
    print(f"\n{len(data['ad_groups'])} 个广告组 · {n_kw} 个关键词 · 校验全过")


if __name__ == "__main__":
    main()
