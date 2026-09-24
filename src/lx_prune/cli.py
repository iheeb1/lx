"""`laya_prune.py` entry point: pipe filter, hook mode, and subcommands."""

import json
import os
import sys

from . import config
from .metrics import (cmd_doctor, cmd_last, cmd_show, cmd_stats, cmd_status,
                      cmd_tune, record)
from .prune import prune

USAGE = """laya_prune.py - trim long shell output before a coding agent reads it, and measure it.

The model can only DROP stretches of routine output. The head, the tail, and anything
matching an error/warning regex are always kept, so a bad score can't hide a failure.

Usage
  laya_prune.py --pipe            filter: raw text on stdin -> pruned text on stdout
  laya_prune.py                   hook mode: JSON hook payload on stdin (Claude-style PostToolUse)
  laya_prune.py stats [--verbose] totals: commands, tokens saved, %, latency, top savers
  laya_prune.py last [N] [--failed] [--verbose]  last N runs (default 15)
  laya_prune.py show ID           pruned output of run ID  (--orig for original, --diff for diff)
  laya_prune.py status            compact one-screen health (stats + doctor + last failure)
  laya_prune.py doctor            check python/laya setup and print fix hints
  laya_prune.py tune [--apply]    learn per-family threshold deltas from history
  laya_prune.py reset             delete all metrics and samples
  NOTE: there is no `watch` subcommand. `watch`, `tail -f`, `less`, `top` are
  interactive (never exit) and must bypass the pruner, not pipe through it.

Env
  LAYA_PRUNE_HOME        data dir              (default ~/.local/share/laya-prune)
  LAYA_PRUNE_CMD         label for the metrics (the plugin sets this to the shell command)
  LAYA_PRUNE_MIN_LINES   only prune output longer than this   (default 120)
  LAYA_PRUNE_CHUNK       min lines per scored chunk            (default 12)
  LAYA_PRUNE_MAX_CHUNKS  target max chunks; more allowed in batches (default 60)
  LAYA_PRUNE_CHUNK_MAX   hard cap on lines per chunk           (default 25)
  LAYA_PRUNE_HEAD/TAIL   lines always kept at each end         (default 20 / 30)
  LAYA_PRUNE_MIN_CONF    keep a chunk if model confidence < x  (default 0.65)
                         auto-tuned per command (noisy logs drop more, listings keep more)
                         unless LAYA_PRUNE_FIXED_CONF=1 is set.
  LAYA_PRUNE_MAX_CHARS   chars of each chunk sent to model     (default 2400, head+tail kept)
  LAYA_PRUNE_COLLAPSE_MIN  min similar lines before collapse   (default 6)
  LAYA_PRUNE_MODE        context: auto|error|debug|verify|minimal (default auto)
                         auto -> error when LAYA_PRUNE_RC!=0. error/debug keep
                         failure neighborhoods + collapse less; verify keeps
                         summaries; minimal is a token diet.
  LAYA_PRUNE_RC          exit code of the pruned command (lx sets this; auto
                         mode switches to error when nonzero)
  LAYA_PRUNE_ERR_CTX     ±lines around failures always kept     (default 8)
                         first failure cluster gets the full window, later
                         (cascading) clusters get a shrunk window
  LAYA_PRUNE_TUNE_FILE   learned threshold deltas JSON (default
                         ~/.config/laya-prune/thresholds.json, via tune --apply)
  LAYA_PRUNE_NO_MODEL=1  heuristics only (no Laya)
  LAYA_PRUNE_NO_COLLAPSE=1  disable the model-free "similar lines" collapse (to test Laya alone)
  LAYA_PRUNE_FIXED_CONF=1  disable per-command confidence auto-tuning
  LAYA_PRUNE_DEBUG=1     print every chunk's Laya verdict (choice, confidence) to stderr
  LAYA_PRUNE_NO_SAMPLES=1  don't store outputs on disk (metrics only)
"""


def response_text(resp):
    if isinstance(resp, str):
        return resp
    if isinstance(resp, dict):
        parts = [str(resp[k]) for k in ("stdout", "stderr") if resp.get(k)]
        return "\n".join(parts) if parts else json.dumps(resp)
    return str(resp or "")


def main(args=None):
    """Dispatch subcommands. Returns an exit code (0 ok, 2 usage error)."""
    if args is None:
        args = sys.argv[1:]
    if args[:1] == ["stats"]:
        return cmd_stats(verbose="--verbose" in args or "-v" in args)
    if args[:1] == ["last"]:
        rest = args[1:]
        only_failed = "--failed" in rest
        verbose = "--verbose" in rest or "-v" in rest
        count = next((int(x) for x in rest if x.isdigit()), 15)
        return cmd_last(count, only_failed=only_failed, verbose=verbose)
    if args[:1] == ["doctor"]:
        return cmd_doctor()
    if args[:1] == ["tune"]:
        return cmd_tune(apply="--apply" in args)
    if args[:1] == ["status"]:
        return cmd_status()
    if args[:1] == ["watch"]:
        print("laya-prune has no `watch` subcommand: watch/tail -f/less never exit, "
              "so a pipe filter would buffer forever and show a black screen.\n"
              "Run the command directly without the pruner instead.", file=sys.stderr)
        return 2
    if args[:1] == ["show"] and len(args) > 1:
        return cmd_show(args[1], args[2] if len(args) > 2 else "")
    if args[:1] == ["reset"]:
        import shutil
        shutil.rmtree(config.HOME, ignore_errors=True)
        return print("cleared", config.HOME)
    if "--help" in args or "-h" in args:
        return print(USAGE)

    if args[:1] and args[0] not in ("--pipe",):
        # Never block on a TTY for an unknown subcommand (was: stdin.read() hang).
        # Hook payloads always arrive via piped stdin, so isatty => usage error.
        # Note: bare `laya_prune.py` with NO args is hook mode (JSON on stdin).
        print(f"unknown command: {args[0]!r}\n", file=sys.stderr)
        print(USAGE, file=sys.stderr)
        return 2

    if sys.stdin.isatty() and "--pipe" in args:
        print("no input on stdin: pipe output in, e.g. `<cmd> 2>&1 | laya_prune.py --pipe`",
              file=sys.stderr)
        return 2
    raw = sys.stdin.read()
    if not raw.strip() and sys.stdin.isatty():
        print(USAGE, file=sys.stderr)
        return 2
    if "--pipe" in args:
        pruned, info = prune(raw)
        record(raw, pruned, info)
        sys.stdout.write(pruned)
        return 0
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        return 0
    if payload.get("hook_event_name") != "PostToolUse" or payload.get("tool_name") != "Bash":
        return 0
    text = response_text(payload.get("tool_response"))
    os.environ.setdefault("LAYA_PRUNE_CMD", str((payload.get("tool_input") or {}).get("command", "")))
    pruned, info = prune(text)
    record(text, pruned, info)
    if pruned != text:
        print(json.dumps({"hookSpecificOutput": {"hookEventName": "PostToolUse",
                                                 "updatedToolOutput": pruned}}))
    return 0
