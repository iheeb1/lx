"""Laya scoring: per-family questions, calibrated confidence, batched verdicts.

NOTE: the typed-decisions checkpoint ships invalid temperatures, so raw
`confidence` is ~0.01-0.2 and uncalibrated (laya logs a RuntimeWarning).
We use `answer_confidence`, falling back to max(probabilities), else raw
confidence. Without this the threshold keeps literally everything.
"""

import os
import re
import sys
import time

from .config import MAX_CHARS
from .modes import effective_min_conf, route_hint, tune_delta_for, venv_hint
from .patterns import KEEPY_RE

QUESTIONS = {
    "keep": {
        "type": "choice",
        "instructions": (
            "This is a slice of shell command output seen by a coding agent. "
            "Is it needed to debug problems or verify the result, or is it routine noise "
            "such as progress bars, repeated log lines, or download logs?"
        ),
        "criteria": {  # neutral A/B keys avoid the true/false label bias Laya documents
            "A": "needed: failures, results, summaries, or facts the agent must see",
            "B": "noise: progress output, repetition, routine logs, no new information",
        },
    }
}

QUESTIONS_DIFF = {
    "keep": {
        "type": "choice",
        "instructions": (
            "This is a slice of a git diff seen by a coding agent. "
            "Hunk headers, added/removed lines, and file names are needed. "
            "Unchanged context lines and repeated index hashes are noise."
        ),
        "criteria": {
            "A": "needed: file paths, hunk headers (@@), +/- lines, errors",
            "B": "noise: unchanged context, index hashes, repeated mode lines",
        },
    }
}

QUESTIONS_LISTING = {
    "keep": {
        "type": "choice",
        "instructions": (
            "This is a slice of a file listing or status output (ls, git status, find). "
            "Every line is usually a distinct fact the agent needs. "
            "Only drop exact duplicates or pure progress noise."
        ),
        "criteria": {
            "A": "needed: file names, status flags, paths (usually keep all)",
            "B": "noise: exact duplicate lines or download-style progress only",
        },
    }
}


def select_questions(cmd_label=""):
    """Diff hunks, listings and generic output each get their own criteria."""
    cmd = (cmd_label or os.getenv("LAYA_PRUNE_CMD", "")).strip()
    if re.search(r"git\s+diff\b|\bdiff\b", cmd, re.I):
        return QUESTIONS_DIFF
    if KEEPY_RE.search(cmd):
        return QUESTIONS_LISTING
    return QUESTIONS


def _model_input(chunk):
    """Keep head+tail of a long chunk so summaries at the end survive."""
    if len(chunk) <= MAX_CHARS:
        return chunk
    head_n = int(MAX_CHARS * 0.65)
    tail_n = MAX_CHARS - head_n
    return chunk[:head_n] + "\n[... chunk truncated for scoring ...]\n" + chunk[-tail_n:]


_agent = None


def _load_agent():
    global _agent
    if _agent is None:
        import laya  # pip install laya (see requirements.txt)

        _agent = laya.load("convaiinnovations/laya", subfolder="typed-decisions")
    return _agent


def reset_agent():
    """Forget the cached agent (tests)."""
    global _agent
    _agent = None


def calibrated_confidence(answer):
    """Best available confidence for a Laya choice answer dict."""
    probs = answer.get("probabilities") or {}
    conf = answer.get("answer_confidence")
    if not isinstance(conf, (int, float)):
        try:
            conf = max(float(probs.get("A", 0)), float(probs.get("B", 0)))
        except Exception:
            conf = 0.0
    if not conf:
        try:
            conf = float(answer.get("confidence", 0.0))
        except Exception:
            conf = 0.0
    return float(conf)


def score_chunks(chunks, info, cmd_label=""):
    """Return list of booleans (True = keep). Conservative on any doubt or failure."""
    info["python"] = sys.executable
    info["min_conf"] = effective_min_conf(cmd_label)
    info["tune_delta"] = round(tune_delta_for(cmd_label), 3)
    route = route_hint(cmd_label)
    if route:
        info["route"] = route
    if not chunks:
        return []
    if os.getenv("LAYA_PRUNE_NO_MODEL") == "1":
        info["model"] = "off"
        return [True] * len(chunks)
    t_load = time.time()
    try:
        agent = _load_agent()
        info["load_ms"] = int((time.time() - t_load) * 1000)
        # Batch so many small chunks don't blow CPU/memory at once.
        questions = select_questions(cmd_label)
        results = []
        t_pred = time.time()
        for b in range(0, len(chunks), 16):
            batch = [{"output": _model_input(c)} for c in chunks[b:b + 16]]
            results.extend(agent.predict_batch(batch, questions))
        info["predict_ms"] = int((time.time() - t_pred) * 1000)
        info["model"] = "laya"
    except Exception as exc:
        info["model"] = f"error: {type(exc).__name__}"
        info["model_error"] = str(exc)[:300]
        info["load_ms"] = int((time.time() - t_load) * 1000)
        hint = ""
        if isinstance(exc, ModuleNotFoundError) and "laya" in str(exc):
            hint = venv_hint(sys.executable)
        print(f"[laya_prune] model unavailable, keeping all: {exc}{hint}", file=sys.stderr)
        return [True] * len(chunks)
    keep, v = [], {"A": 0, "B": 0, "low_conf": 0}
    thresh = info["min_conf"]
    for i, r in enumerate(results):
        try:
            a = r["answers"]["keep"]
            choice = a.get("choice", "A")
            conf = calibrated_confidence(a)
        except Exception:
            choice, conf = "A", 0.0
        k = choice == "A" or conf < thresh
        v[choice if choice in ("A", "B") else "A"] += 1
        v["low_conf"] += conf < thresh
        if os.getenv("LAYA_PRUNE_DEBUG") == "1":
            print(f"[laya_prune] chunk {i:>3}: {choice} conf={conf:.2f} thr={thresh:.2f} -> "
                  f"{'keep' if k else 'DROP'}  | {chunks[i][:60]!r}", file=sys.stderr)
        keep.append(k)
    info["verdicts"] = v
    return keep
