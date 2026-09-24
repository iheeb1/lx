"""lx_prune - unified laya+rtk shell-output pruning toolkit.

Modules:
  config    env-driven settings and data paths
  patterns  keep/drop regexes (errors, summaries, command families)
  modes     context modes, thresholds, learned tune deltas
  collapse  model-free shrinking (dedupe + similar-line collapse)
  model     Laya scoring (questions, confidence, batched verdicts)
  prune     pipeline orchestration + failure-cluster windows
  metrics   run records, stats/last/show/status/doctor/tune commands
  cli       `laya_prune.py` entry point (pipe + hook + subcommands)
  runner    `lx` unified entry point (RTK routing + Laya second pass)
"""

__version__ = "1.0.0"

__all__ = [
    "config", "patterns", "modes", "collapse", "model",
    "prune", "metrics", "cli", "runner",
]
