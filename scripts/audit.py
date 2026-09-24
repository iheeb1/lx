#!/usr/bin/env python3
"""Safety audit: did the pruner ever drop a line that matches the must-keep regex?
Run from the repo root after real use:  ./scripts/audit.py  (or .venv/bin/python scripts/audit.py)"""
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / "src"))
from lx_prune.metrics import RUNS
from lx_prune.patterns import MUST_KEEP

bad = total = 0
for orig in sorted(RUNS.glob("*.orig.txt")):
    pruned = set(orig.with_name(orig.name.replace(".orig.", ".pruned.")).read_text().splitlines())
    lost = [l for l in orig.read_text().splitlines() if MUST_KEEP.search(l) and l not in pruned]
    total += 1
    if lost:
        bad += 1
        print(f"{orig.name.split('.')[0]}: {len(lost)} important-looking line(s) missing, e.g. {lost[0][:100]!r}")
print(f"checked {total} pruned run(s); {bad} with missing important lines")
sys.exit(1 if bad else 0)
