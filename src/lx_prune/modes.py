"""Context modes, command families, learned thresholds, routing hints."""

import json
import os
import re

from .config import MIN_CONF, TUNE_FILE, VENV_PY
from .patterns import INTERACTIVE_RE, KEEPY_RE, NOISY_RE, RTK_OWNED_RE

# Context modes: what the agent needs depends on why it ran the command.
#   auto    (default) rc!=0 -> error, else default behavior
#   error   debugging a failure: keep error neighborhoods, collapse less
#   debug   actively debugging: keep INFO/DEBUG logs, collapse less
#   verify  green-run check: keep summaries/verdicts, drop verbose progress
#   minimal token diet: aggressive drops, tiny context window
MODES = ("auto", "error", "debug", "verify", "minimal")
ERR_CTX = int(os.getenv("LAYA_PRUNE_ERR_CTX", "8"))  # ±lines around failures always kept


def resolve_mode(cmd_label=""):
    """Return (mode, rc). auto -> error when the command failed (rc!=0)."""
    want = os.getenv("LAYA_PRUNE_MODE", "auto").strip().lower()
    if want not in MODES:
        want = "auto"
    try:
        rc = int(os.getenv("LAYA_PRUNE_RC", "0"))
    except ValueError:
        rc = 0
    if want == "auto" and rc != 0:
        return "error", rc
    return want, rc


def mode_adjust(mode):
    """Return (conf_delta, collapse_mult, ctx) for a mode."""
    if mode == "error":
        return (0.15, 2, max(ERR_CTX, 10))
    if mode == "debug":
        return (0.10, 2, max(ERR_CTX, 6))
    if mode == "verify":
        return (-0.05, 1, 4)
    if mode == "minimal":
        return (-0.25, 1, 2)
    return (0.0, 1, ERR_CTX)


def family_of(cmd_label=""):
    """noisy | keepy | other — mirrors effective_min_conf priority (keepy wins)."""
    cmd = (cmd_label or os.getenv("LAYA_PRUNE_CMD", "")).strip()
    if KEEPY_RE.search(cmd):
        return "keepy"
    if NOISY_RE.search(cmd):
        return "noisy"
    return "other"


_tune_cache = None


def reset_tune_cache():
    """Forget the cached tune file (tests, or after `tune --apply`)."""
    global _tune_cache
    _tune_cache = None


def load_tune():
    """Learned per-family threshold deltas from `tune --apply`.

    Returns {} when no tune file exists. Values are clamped to ±0.10 at use.
    """
    global _tune_cache
    if _tune_cache is not None:
        return _tune_cache
    try:
        _tune_cache = {k: float(v) for k, v in json.loads(TUNE_FILE.read_text()).items()}
    except Exception:
        _tune_cache = {}
    return _tune_cache


def tune_delta_for(cmd_label=""):
    """Clamped learned delta for this command's family (0.0 when untuned)."""
    return max(-0.10, min(0.10, float(load_tune().get(family_of(cmd_label), 0.0))))


def route_hint(cmd_label=""):
    """Suggest `rtk ...` when RTK owns the command deterministically, else ''."""
    cmd = (cmd_label or os.getenv("LAYA_PRUNE_CMD", "")).strip()
    if not cmd or not RTK_OWNED_RE.search(cmd):
        return ""
    first = re.split(r"[|;&]", cmd.strip())[0].strip()
    return f"rtk {first}"[:120]


def effective_min_conf(cmd_label="", mode=None):
    """Per-command confidence threshold. Higher = safer (keep on doubt).

    Command family sets the base (listings keep, noisy logs drop), the tune
    file shifts it from your own history (`tune --apply`), then the context
    mode shifts it (error/debug keep more, verify/minimal drop more).
    """
    if os.getenv("LAYA_PRUNE_FIXED_CONF") == "1":
        return MIN_CONF
    if mode is None:
        mode, _ = resolve_mode(cmd_label)
    base = MIN_CONF
    cmd = (cmd_label or os.getenv("LAYA_PRUNE_CMD", "")).strip()
    if INTERACTIVE_RE.search(cmd):
        return 1.01  # never drop: caller should bypass instead
    if KEEPY_RE.search(cmd):
        base = max(base, 0.80)
    elif NOISY_RE.search(cmd):
        base = min(base, 0.50)
    if mode == "minimal":
        return min(base, 0.40)
    return min(0.95, max(0.30, base + tune_delta_for(cmd_label) + mode_adjust(mode)[0]))


def venv_hint(python_exe):
    """Human hint when the running interpreter lacks the `laya` module."""
    return (f" [hint: {python_exe} has no 'laya' module. "
            f"Run with the venv python, e.g. LAYA_PRUNE_PY={VENV_PY}]")
