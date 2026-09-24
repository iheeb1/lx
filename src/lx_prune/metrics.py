"""Run records (metrics.jsonl) and read-only commands: stats/last/show/status/doctor/tune."""

import difflib
import json
import os
import re
import sys
import time
from collections import defaultdict
from datetime import datetime

from . import config
from .config import HOME, KEEP_SAMPLES, LOG, RUNS, TUNE_FILE
from .model import QUESTIONS, _load_agent
from .modes import family_of


def est_tokens(s):
    """Rough estimate (chars/4). Not the real tokenizer of your model."""
    return (len(s) + 3) // 4


def record(original, pruned, info, stage="laya"):
    """Append a run record; persist samples when output changed. Returns run id."""
    label = os.getenv("LAYA_PRUNE_CMD", "")
    rid = datetime.now().strftime("%Y%m%d-%H%M%S-") + f"{int(time.time() * 1000) % 1000:03d}"
    changed = pruned != original
    rec = {
        "id": rid,
        "ts": datetime.now().isoformat(timespec="seconds"),
        "cmd": " ".join(label.split())[:200],
        "stage": stage,
        "lines_in": original.count("\n") + 1 if original else 0,
        "lines_out": pruned.count("\n") + 1 if pruned else 0,
        "tok_in": est_tokens(original),
        "tok_out": est_tokens(pruned),
        "changed": changed,
        **info,
    }
    return _persist_record(rec, original, pruned)


def _persist_record(rec, original, pruned):
    try:
        HOME.mkdir(parents=True, exist_ok=True)
        with open(LOG, "a") as f:
            f.write(json.dumps(rec) + "\n")
        _rotate_log()
        if rec["changed"] and os.getenv("LAYA_PRUNE_NO_SAMPLES") != "1":
            RUNS.mkdir(parents=True, exist_ok=True)
            rid = rec["id"]
            (RUNS / f"{rid}.orig.txt").write_text(original)
            (RUNS / f"{rid}.pruned.txt").write_text(pruned)
            old = sorted(RUNS.glob("*.orig.txt"))[:-KEEP_SAMPLES]
            for p in old:
                p.unlink(missing_ok=True)
                (RUNS / p.name.replace(".orig.", ".pruned.")).unlink(missing_ok=True)
    except OSError as exc:  # metrics must never break the tool
        print(f"[laya_prune] metrics not saved: {exc}", file=sys.stderr)
    return rec["id"]


def _rotate_log():
    """Cap metrics.jsonl at MAX_LOG_LINES (drops oldest 25% when exceeded)."""
    try:
        lines = LOG.read_text().splitlines()
    except OSError:
        return
    if len(lines) > config.MAX_LOG_LINES:
        LOG.write_text("\n".join(lines[-int(config.MAX_LOG_LINES * 0.75):]) + "\n")


def read_log():
    if not LOG.exists():
        return []
    out = []
    for line in LOG.read_text().splitlines():
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    return out


def n(x):
    return f"{x:,}"


def short_cmd(cmd, width=32):
    """First real command for top-savers: strip pipes/redirects, keep 1-2 words."""
    if not (cmd or "").strip():
        return "?"
    first = re.split(r"[|;&]", cmd.strip())[0]
    first = re.sub(r"^\s*(?:\/\S+\/)?(python\d?|uv|pip|npm|docker|git|ls|rg|php|cat)\b", r"\1", first)
    words = first.strip().split()
    if not words:
        return "?"
    base = os.path.basename(words[0]) if "/" in words[0] else words[0]
    key = base if len(words) == 1 else f"{base} {words[1]}"
    return key[:width]


def cmd_stats(verbose=False):
    recs = read_log()
    if not recs:
        print("No runs recorded yet.")
        return
    pruned = [r for r in recs if r.get("changed")]
    t_in = sum(r.get("tok_in", 0) for r in recs)
    t_out = sum(r.get("tok_out", 0) for r in recs)
    saved = t_in - t_out
    pct = (saved / t_in * 100) if t_in else 0
    p_in = sum(r.get("tok_in", 0) for r in pruned)
    p_pct = ((p_in - sum(r.get("tok_out", 0) for r in pruned)) / p_in * 100) if p_in else 0
    lat = [r.get("ms", 0) for r in pruned]
    errs = [r for r in recs if str(r.get("model", "")).startswith("error")]
    loads = [r.get("load_ms", 0) for r in recs if isinstance(r.get("load_ms"), int)]
    preds = [r.get("predict_ms", 0) for r in recs if isinstance(r.get("predict_ms"), int)]
    print(f"laya-prune  since {recs[0].get('ts', '?')}  ({LOG})")
    print(f"  commands seen        {n(len(recs))}")
    print(f"  pruned               {n(len(pruned))}  ({len(pruned) / len(recs) * 100:.0f}%)")
    print(f"  est. tokens in       {n(t_in)}")
    print(f"  est. tokens out      {n(t_out)}")
    print(f"  est. tokens saved    {n(saved)}  ({pct:.1f}% overall, {p_pct:.1f}% on pruned runs)")
    if lat:
        print(f"  prune latency        avg {sum(lat) / len(lat) / 1000:.2f}s  max {max(lat) / 1000:.2f}s", end="")
        if loads and preds:
            print(f"  (load avg {sum(loads) / len(loads) / 1000:.1f}s, predict avg "
                  f"{sum(preds) / len(preds) / 1000:.1f}s)")
        else:
            print()
    if errs:
        print(f"  model failures       {len(errs)} run(s) fell back to keep-everything")
        for r in errs[-3:]:
            print(f"    {r.get('id', '?')}  {r.get('model')}  {str(r.get('model_error', ''))[:100]}"
                  f"  py={os.path.basename(str(r.get('python', '?')))}")
        print(f"  fix: LAYA_PRUNE_PY={config.VENV_PY} (see: laya_prune.py doctor)")
    by = defaultdict(lambda: [0, 0])
    for r in pruned:
        key = short_cmd(r.get("cmd", ""))
        by[key][0] += 1
        by[key][1] += r.get("tok_in", 0) - r.get("tok_out", 0)
    if by:
        print("  top savers")
        for k, (runs, s) in sorted(by.items(), key=lambda kv: -kv[1][1])[:8]:
            print(f"    {k:<32} {runs:>4} runs  saved {n(s)}")
    if verbose:
        print(f"  python               {sys.executable}")
        print(f"  thresholds           MIN_CONF={config.MIN_CONF} CHUNK={config.CHUNK}/{config.CHUNK_MAX} "
              f"MAX_CHUNKS={config.MAX_CHUNKS} MAX_CHARS={config.MAX_CHARS} "
              f"COLLAPSE_MIN={config.COLLAPSE_MIN}")
    print("  (token counts are chars/4 estimates, not your model's tokenizer)")


def cmd_last(count, only_failed=False, verbose=False):
    recs = read_log()
    if only_failed:
        recs = [r for r in recs if str(r.get("model", "")).startswith("error")]
    recs = recs[-count:]
    if not recs:
        print("No runs recorded yet." + (" (no failures)" if only_failed else ""))
        return
    print(f"{'id':<19} {'cmd':<34} {'tok in':>8} {'out':>8} {'saved':>6} {'ms':>6}  model")
    for r in recs:
        tok_in, tok_out = r.get("tok_in", 0), r.get("tok_out", 0)
        saved = (1 - tok_out / tok_in) * 100 if tok_in else 0
        mark = f"{saved:5.0f}%" if r.get("changed") else "     -"
        print(f"{r.get('id', '?'):<19} {r.get('cmd', '')[:33]:<34} {tok_in:>8} {tok_out:>8} "
              f"{mark:>6} {r.get('ms', 0):>6}  {r.get('model', '?')}")
        if verbose and (r.get("model_error") or r.get("verdicts")):
            print(f"      thr={r.get('min_conf', '?')} mode={r.get('mode', '?')} verdicts={r.get('verdicts')} "
                  f"load={r.get('load_ms')}ms pred={r.get('predict_ms')}ms "
                  f"err={str(r.get('model_error', ''))[:160]}")


def cmd_show(rid, mode):
    orig, pr = RUNS / f"{rid}.orig.txt", RUNS / f"{rid}.pruned.txt"
    if not orig.exists():
        sys.exit(f"No saved sample for {rid} (unchanged run, too old, or samples disabled). Try: last")
    rec = next((r for r in read_log() if r["id"] == rid), {})
    print(f"# {rid}  {rec.get('cmd', '')}")
    print(f"# lines {rec.get('lines_in')}->{rec.get('lines_out')}  "
          f"similar-lines collapsed {rec.get('collapsed', 0)}  "
          f"chunks {rec.get('chunks')} (regex-kept {rec.get('forced')}, model-scored {rec.get('scored')}, "
          f"dropped {rec.get('dropped_chunks')})  model={rec.get('model')}  verdicts={rec.get('verdicts')}")
    print(f"# thr={rec.get('min_conf', '?')} mode={rec.get('mode', '?')} "
          f"load={rec.get('load_ms', '?')}ms "
          f"pred={rec.get('predict_ms', '?')}ms py={rec.get('python', '?')}"
          + (f" rc={rec.get('rc', '')}" if rec.get("rc") else "")
          + (f" err={rec.get('model_error', '')}" if rec.get("model_error") else "") + "\n")
    if mode == "--orig":
        print(orig.read_text())
    elif mode == "--diff":
        for line in difflib.unified_diff(orig.read_text().splitlines(), pr.read_text().splitlines(),
                                         "original", "pruned", lineterm="", n=1):
            print(line)
    else:
        print(pr.read_text())


def cmd_status():
    """One-screen health: python/laya OK?, totals, last failure. Never reads stdin."""
    print(f"python: {sys.executable}")
    try:
        import laya  # noqa: F401
        _load_agent()
        print("laya: OK")
    except Exception as exc:
        print(f"laya: FAIL ({type(exc).__name__}: {exc})")
        print(f"fix: export LAYA_PRUNE_PY={config.VENV_PY}")
    recs = read_log()
    if not recs:
        print("runs: none yet")
        return
    pruned = [r for r in recs if r.get("changed")]
    t_in = sum(r.get("tok_in", 0) for r in recs)
    saved = t_in - sum(r.get("tok_out", 0) for r in recs)
    errs = [r for r in recs if str(r.get("model", "")).startswith("error")]
    print(f"runs: {len(recs)} seen, {len(pruned)} pruned, ~{saved:,} tokens saved")
    if errs:
        last = errs[-1]
        print(f"last failure: {last.get('id', '?')} {last.get('model')} "
              f"{str(last.get('model_error', ''))[:120]}")
    else:
        print("failures: none")


def cmd_tune(apply=False):
    """Learn per-family threshold deltas from your own metrics history.

    For each family (noisy/keepy/other) over runs scored by the model:
      drop_rate = dropped_chunks / scored  (how much the model dared to drop)
      saved%    = tokens saved on pruned runs
      unsure    = low_conf verdicts / scored (model guessing)
    Rules (conservative, suggestions only unless --apply):
      drop_rate < 10% and saved < 10%  -> -0.05 (too timid, drop more)
      unsure > 50%                     -> +0.05 (model guessing, keep more)
      otherwise                        ->  0.00
    Needs >=3 scored runs per family; deltas clamp to ±0.10 in the file.
    """
    recs = [r for r in read_log()
            if r.get("model") == "laya" and (r.get("scored") or 0) > 0]
    fams = defaultdict(list)
    for r in recs:
        fams[family_of(r.get("cmd", ""))].append(r)
    sugg = {}
    print(f"{'family':<8} {'runs':>5} {'drop%':>7} {'saved%':>8} {'unsure%':>8}  suggestion")
    for fam in ("noisy", "keepy", "other"):
        rs = fams.get(fam, [])
        if len(rs) < 3:
            print(f"{fam:<8} {len(rs):>5}       -        -        -  need >=3 scored runs")
            sugg[fam] = 0.0
            continue
        scored = sum(r.get("scored", 0) for r in rs)
        dropped = sum(r.get("dropped_chunks", 0) for r in rs)
        drop_rate = dropped / scored if scored else 0
        pruned = [r for r in rs if r.get("changed")]
        saved = ((sum(r.get("tok_in", 0) for r in pruned)
                  - sum(r.get("tok_out", 0) for r in pruned))
                 / (sum(r.get("tok_in", 0) for r in pruned) or 1) * 100) if pruned else 0
        unsure = (sum((r.get("verdicts") or {}).get("low_conf", 0) for r in rs)
                  / scored if scored else 0)
        if drop_rate < 0.10 and saved < 10:
            d = -0.05
        elif unsure > 0.50:
            d = 0.05
        else:
            d = 0.0
        sugg[fam] = d
        print(f"{fam:<8} {len(rs):>5} {drop_rate * 100:>6.1f}% {saved:>7.1f}% {unsure * 100:>7.1f}%  {d:+.2f}")
    if apply:
        try:
            cur = {}
            try:
                cur = {k: float(v) for k, v in json.loads(TUNE_FILE.read_text()).items()}
            except Exception:
                pass
            for fam, d in sugg.items():
                cur[fam] = round(max(-0.10, min(0.10, cur.get(fam, 0.0) + d)), 3)
            TUNE_FILE.parent.mkdir(parents=True, exist_ok=True)
            TUNE_FILE.write_text(json.dumps(cur, indent=2))
            print(f"wrote {TUNE_FILE}: {cur}")
        except OSError as exc:
            sys.exit(f"cannot write tune file: {exc}")
    else:
        print(f"dry run (no file written). Apply with: laya_prune.py tune --apply  [{TUNE_FILE}]")
    # Coverage: the learner starves when the model rarely fires (heuristics +
    # force-keep resolve most runs first). This table shows where runs actually
    # go per family, so you can see whether tune will ever get data.
    all_recs = read_log()
    cov = defaultdict(list)
    for r in all_recs:
        cov[family_of(r.get("cmd", ""))].append(r)
    print(f"\n{'family':<8} {'runs':>5} {'short%':>7} {'collapse%':>9} {'model%':>7}")
    for fam in ("noisy", "keepy", "other"):
        rs = cov.get(fam, [])
        if not rs:
            print(f"{fam:<8} {0:>5}       -         -       -")
            continue
        short = sum(1 for r in rs if r.get("model") == "skipped" and not r.get("changed"))
        coll = sum(1 for r in rs if (r.get("collapsed") or 0) > 0)
        mod = sum(1 for r in rs if r.get("model") == "laya")
        print(f"{fam:<8} {len(rs):>5} {short / len(rs) * 100:>6.1f}% {coll / len(rs) * 100:>8.1f}% "
              f"{mod / len(rs) * 100:>6.1f}%")


def cmd_doctor():
    print(f"python: {sys.executable}")
    try:
        import laya  # noqa: F401
        agent = _load_agent()
        print("laya: import OK")
        r = agent.predict_batch([{"output": "hello world log line 1\n" * 3}], QUESTIONS)
        print(f"laya: predict OK -> {r[0]['answers']['keep']}")
    except Exception as exc:
        print(f"laya: FAIL ({type(exc).__name__}: {exc})")
        print("fix: use the venv python that has laya installed:")
        print(f"  export LAYA_PRUNE_PY={config.VENV_PY}")
        print(f"  # or: {config.VENV_PY.parent / 'pip'} install laya")
    print(f"config: MIN_CONF={config.MIN_CONF} CHUNK={config.CHUNK}/{config.CHUNK_MAX} "
          f"MAX_CHUNKS={config.MAX_CHUNKS} MAX_CHARS={config.MAX_CHARS} "
          f"COLLAPSE_MIN={config.COLLAPSE_MIN} HEAD={config.HEAD} TAIL={config.TAIL}")
    print(f"data: LOG={LOG} RUNS={RUNS}")
    print("plugin default PY=python3 SCRIPT=~/.config/opencode/laya_prune.py "
          "(override via LAYA_PRUNE_PY / LAYA_PRUNE_SCRIPT)")
