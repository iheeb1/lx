"""`lx` unified entry point: RTK routing + Laya second pass.

Kept import-light so `--help` stays instant; the model loads lazily.
The venv re-exec preamble lives in the `lx` shim, not here.
"""

import os
import shlex
import shutil
import subprocess
import sys
import time

from . import config, metrics
from .cli import main as prune_main
from .patterns import INTERACTIVE_RE
from .prune import prune_text

USAGE = """lx - unified laya+rtk command runner.

Routes each command through the best stage, executes it exactly once,
preserves its exit code, and records metrics with a `stage` label:

  bypass    interactive / never-ending (watch, tail -f, ...) -> raw, untouched
  rtk       RTK rewrote it and output stayed short -> RTK output as-is
  rtk+laya  RTK rewrote it but output stayed long -> Laya second pass
  laya      no RTK rewrite (or rtk missing) -> raw + Laya prune
  raw       output too short to prune -> unchanged

Usage:
  lx <command...>          run a shell command through the pipeline
  lx pipe                  read stdin, prune, print (like laya_prune.py --pipe)
  lx stats|last|show|status|doctor|tune|gain|pipe   observability & utils
  lx gain [--history]      rtk gain + laya stats side by side
  lx tune [--apply]        learn per-family thresholds from your history

Env:
  LX_RTK_BIN   rtk binary (default: first `rtk` on PATH, '' to force laya-only)
  LX_NO_RTK=1  force laya-only mode (graceful fallback, also when rtk missing)
  LX_NO_LAYA=1 skip the Laya second pass (RTK-or-raw only)
  LX_TIMEOUT   seconds for `rtk hook check` (default 5)
  LAYA_PRUNE_MODE=LX passes the command's exit code through, so auto mode
  LAYA_PRUNE_RC keeps error neighborhoods on failure; override with
  LAYA_PRUNE_MODE=error|debug|verify|minimal explicitly.
"""

PASSTHROUGH = {"stats", "last", "show", "status", "doctor", "tune", "reset"}
RTK_TIMEOUT = float(os.getenv("LX_TIMEOUT", "5"))


def rtk_bin():
    """rtk binary path, or None for forced/missing laya-only mode."""
    forced = os.getenv("LX_RTK_BIN", None)
    if forced is not None:
        return forced or None
    if os.getenv("LX_NO_RTK") == "1":
        return None
    return shutil.which("rtk")


def rtk_rewrite(cmd, rtk):
    """Dry-run router. Returns rewritten command or '' (never executes)."""
    if not rtk:
        return ""
    try:
        p = subprocess.run([rtk, "hook", "check", cmd], capture_output=True,
                           text=True, timeout=RTK_TIMEOUT)
    except Exception:
        return ""
    if p.returncode == 0 and p.stdout.strip() and "No rewrite" not in p.stdout:
        return p.stdout.strip()
    return ""


def run_capture(cmd):
    """Run via sh -c, capture combined output, return (rc, output)."""
    p = subprocess.run(["sh", "-c", cmd], capture_output=True, text=True)
    out = (p.stdout or "") + (p.stderr or "")
    return p.returncode, out


def emit(cmd_label, output, stage, rc=0):
    """Prune (unless bypass/raw) + record + print. Returns pruned text.

    rc feeds LAYA_PRUNE_RC so auto mode switches to error on failure --
    a failing test keeps its error neighborhood, a passing one stays lean.
    """
    os.environ["LAYA_PRUNE_RC"] = str(rc)
    if stage == "bypass":
        sys.stdout.write(output)
        return output
    if os.getenv("LX_NO_LAYA") == "1":
        os.environ["LAYA_PRUNE_CMD"] = cmd_label
        metrics.record(output, output, {"model": "off", "chunks": 0}, stage=stage)
        sys.stdout.write(output)
        return output
    pruned, info = prune_text(output, cmd_label)
    metrics.record(output, pruned, info, stage=stage if pruned != output or stage != "laya"
                   else ("raw" if info.get("model") == "skipped" else stage))
    sys.stdout.write(pruned)
    return pruned


def run_command(cmd):
    """Route -> execute once -> filter. Returns the command's exit code."""
    # 1. bypass: interactive / never-ending must not be buffered
    if INTERACTIVE_RE.search(cmd):
        p = subprocess.run(["sh", "-c", cmd])
        return p.returncode
    rtk = rtk_bin()
    rewritten = rtk_rewrite(cmd, rtk) if rtk else ""
    if rewritten:
        t0 = time.time()
        rc, out = run_capture(rewritten)
        exec_ms = int((time.time() - t0) * 1000)
        # Laya second pass only when RTK output is still long
        if len(out.splitlines()) > config.MIN_LINES:
            emit(cmd, out, stage="rtk+laya", rc=rc)
        else:
            os.environ["LAYA_PRUNE_CMD"] = cmd
            metrics.record(out, out, {"model": "rtk-passthrough", "chunks": 0, "ms": exec_ms,
                                      "route": rewritten}, stage="rtk")
            sys.stdout.write(out)
        return rc
    # laya branch (also the graceful fallback when rtk is missing)
    rc, out = run_capture(cmd)
    emit(cmd, out, stage="laya", rc=rc)
    return rc


def main(argv):
    """Dispatch observability subcommands or run a shell command."""
    if not argv or argv[0] in ("-h", "--help"):
        print(USAGE)
        return 0
    if argv[0] == "pipe":
        data = sys.stdin.read()
        emit(os.getenv("LAYA_PRUNE_CMD", ""), data, stage="laya")
        return 0
    if argv[0] == "gain":
        rtk = rtk_bin()
        if rtk:
            p = subprocess.run([rtk, "gain"] + argv[1:])
            rc = p.returncode
        else:
            print("(rtk not found: laya-only mode)", file=sys.stderr)
            rc = 0
        print()
        metrics.cmd_stats()
        return rc
    if argv[0] in PASSTHROUGH:
        return prune_main(argv)
    # otherwise: a shell command. shlex.join preserves quoting, so
    # `lx sh -c 'exit 7'` runs exactly what the user typed.
    cmd = shlex.join(argv)
    try:
        return run_command(cmd)
    except BrokenPipeError:
        return 0
