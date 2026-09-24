#!/usr/bin/env python3
"""Backward-compatible entry point. Real code lives in src/lx_prune/.

`import laya_prune` keeps working (audit.py, tests, deployed copies):
common names are re-exported below.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / "src"))

from lx_prune.cli import USAGE, main  # noqa: E402
from lx_prune.config import (CHUNK, CHUNK_MAX, COLLAPSE_MIN, HEAD, HOME,  # noqa: E402,F401
                             KEEP_SAMPLES, LOG, MAX_CHARS, MAX_CHUNKS,
                             MIN_CONF, MIN_LINES, RUNS, TAIL)
from lx_prune.metrics import record  # noqa: E402,F401
from lx_prune.modes import effective_min_conf  # noqa: E402,F401
from lx_prune.patterns import MUST_KEEP  # noqa: E402,F401
from lx_prune.prune import error_windows, prune, prune_text, ranked_windows  # noqa: E402,F401

__doc__ = USAGE

if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BrokenPipeError:  # e.g. `laya_prune.py show ID | head`
        pass
