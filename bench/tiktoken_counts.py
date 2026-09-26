#!/usr/bin/env python3
"""Exact token counts for the benchmark, so published numbers don't rest on
lx's own estimator.

    pip install tiktoken
    python3 bench/tiktoken_counts.py            # after `make bench` (and h2h)

Adds "exact" blocks to bench/out/results.json and bench/out/h2h.json and
writes bench/out/tokstats.json (exact counts of every raw corpus capture,
used to score the offline estimator).
"""
import json
import os
import re
import sys

try:
    import tiktoken
except ImportError:
    sys.exit("tiktoken not installed: pip install tiktoken")

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "bench", "out")
CORPUS = os.path.join(ROOT, "testdata", "corpus")
ENCS = {name: tiktoken.get_encoding(name) for name in ("cl100k_base", "o200k_base")}
ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")


def count(text):
    return {name: len(enc.encode(text, disallowed_special=())) for name, enc in ENCS.items()}


def read(path):
    with open(path, encoding="utf-8", errors="replace") as f:
        return f.read()


def main():
    # 1. every raw capture (ANSI stripped the way lx strips it before estimating)
    stats = []
    for cat in sorted(os.listdir(CORPUS)):
        d = os.path.join(CORPUS, cat)
        if not os.path.isdir(d):
            continue
        for name in sorted(os.listdir(d)):
            if not name.endswith(".txt"):
                continue
            raw = read(os.path.join(d, name))
            c = count(ANSI.sub("", raw))
            stats.append([f"{cat}/{name}", len(raw.encode()), c["cl100k_base"], c["o200k_base"]])
    with open(os.path.join(OUT, "tokstats.json"), "w") as f:
        json.dump(stats, f)
    print(f"tokstats: {len(stats)} captures")

    # 2. corpus bench: raw vs lx view
    rp = os.path.join(OUT, "results.json")
    if os.path.exists(rp):
        res = json.load(open(rp))
        tot = {"raw": {"cl100k_base": 0, "o200k_base": 0}, "lx": {"cl100k_base": 0, "o200k_base": 0}}
        for row in res["cases"]:
            raw = read(os.path.join(CORPUS, row["category"], row["name"] + ".txt"))
            view = read(os.path.join(OUT, row["view"]))
            row["exact"] = {"raw": count(raw), "lx": count(view)}
            for k in ("raw", "lx"):
                for e in ENCS:
                    tot[k][e] += row["exact"][k][e]
        res["exact_totals"] = tot
        json.dump(res, open(rp, "w"), indent=2)
        for e in ENCS:
            r, l = tot["raw"][e], tot["lx"][e]
            print(f"corpus {e}: raw {r} → lx {l} ({100 * (r - l) / r:.1f}% saved)")

    # 3. head-to-head
    hp = os.path.join(OUT, "h2h.json")
    if os.path.exists(hp):
        rows = json.load(open(hp))
        for row in rows:
            for k in ("raw", "rtk", "lx"):
                row[k]["exact"] = count(read(os.path.join(OUT, row[k]["file"])))
        json.dump(rows, open(hp, "w"), indent=2)
        print(f"h2h: {len(rows)} cases")


if __name__ == "__main__":
    main()
