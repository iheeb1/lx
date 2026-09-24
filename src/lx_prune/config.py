"""Environment-driven settings and data paths. No logic, just knobs."""

import os
from pathlib import Path

# Repo root (src/lx_prune -> <root>). Used for venv lookup and hints.
TOOL_HOME = Path(__file__).resolve().parent.parent.parent


def _find_venv():
    """Venv python with `laya`. Explicit LAYA_PRUNE_VENV wins; then the local
    .venv, then the default checkout location (covers deployed copies under
    ~/.config/opencode which ship without a venv)."""
    override = os.getenv("LAYA_PRUNE_VENV")
    if override:
        p = Path(override)
        return p if p.is_file() else p / "bin" / "python"
    for base in (TOOL_HOME, Path.home() / "laya_based_noise_removal_tool"):
        cand = base / ".venv" / "bin" / "python"
        if cand.exists():
            return cand
    return TOOL_HOME / ".venv" / "bin" / "python"  # best guess for hints


VENV_PY = _find_venv()

MIN_LINES = int(os.getenv("LAYA_PRUNE_MIN_LINES", "120"))
CHUNK = int(os.getenv("LAYA_PRUNE_CHUNK", "12"))
HEAD = int(os.getenv("LAYA_PRUNE_HEAD", "20"))
TAIL = int(os.getenv("LAYA_PRUNE_TAIL", "30"))
MIN_CONF = float(os.getenv("LAYA_PRUNE_MIN_CONF", "0.65"))
MAX_CHUNKS = int(os.getenv("LAYA_PRUNE_MAX_CHUNKS", "60"))  # target; extra chunks run in batches
CHUNK_MAX = int(os.getenv("LAYA_PRUNE_CHUNK_MAX", "25"))  # hard cap so big logs keep small chunks
MAX_CHARS = int(os.getenv("LAYA_PRUNE_MAX_CHARS", "2400"))  # head+tail slice, not blind truncate
COLLAPSE_MIN = int(os.getenv("LAYA_PRUNE_COLLAPSE_MIN", "6"))
KEEP_SAMPLES = 200
MAX_LOG_LINES = int(os.getenv("LAYA_PRUNE_MAX_LOG", "5000"))  # metrics.jsonl rotation cap

HOME = Path(os.getenv("LAYA_PRUNE_HOME", Path.home() / ".local/share/laya-prune"))
LOG = HOME / "metrics.jsonl"
RUNS = HOME / "runs"

TUNE_FILE = Path(os.getenv("LAYA_PRUNE_TUNE_FILE",
                           Path.home() / ".config" / "laya-prune" / "thresholds.json"))
