#!/usr/bin/env bash
# devlog-demo.sh - laya vs rtk on a real Symfony dev.log, one shot.
#   ./scripts/devlog-demo.sh [path/to/dev.log] [python]
# Isolated metrics (real history untouched). Model runs on a 2000-line tail
# slice so the demo stays in minutes, not tens of minutes, on CPU.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
DEVLOG="${1:-/home/iheb/sv2c-core/var/log/dev.log}"
if [ -n "${2:-}" ]; then PY="$2"; elif [ -x "$REPO/.venv/bin/python" ]; then PY="$REPO/.venv/bin/python"; else PY=python3; fi
S="$REPO/laya_prune.py"
T=$(mktemp -d)
export LAYA_PRUNE_HOME="$T/home"
RTK="$(command -v rtk || true)"
[ -f "$DEVLOG" ] || { echo "no such file: $DEVLOG"; exit 1; }

echo "# dev.log demo: $DEVLOG ($(wc -l < "$DEVLOG") lines, $(du -h "$DEVLOG" | cut -f1))"
echo "# workdir $T (isolated metrics)"
echo

echo "## 1. heuristics only, full file (no model)"
time (LAYA_PRUNE_CMD='dev.log' LAYA_PRUNE_NO_MODEL=1 "$PY" "$S" --pipe < "$DEVLOG" > "$T/collapse.txt" 2>/dev/null)
echo "raw -> collapse: $(wc -l < "$DEVLOG") -> $(wc -l < "$T/collapse.txt") lines"

echo
echo "## 2. full model (error mode) on tail -2000 slice"
tail -n 2000 "$DEVLOG" > "$T/slice.txt"
time (LAYA_PRUNE_CMD='dev.log slice' LAYA_PRUNE_MODE=error "$PY" "$S" --pipe < "$T/slice.txt" > "$T/model.txt" 2>"$T/model.err")
echo "slice -> model: $(wc -l < "$T/slice.txt") -> $(wc -l < "$T/model.txt") lines"
tail -n 2 "$T/model.err" | grep -v Fetching || true

echo
echo "## 3. rtk for reference"
if [ -n "$RTK" ]; then
  time (rtk log "$DEVLOG" > "$T/rtk.txt" 2>/dev/null)
  echo "raw -> rtk log: $(wc -l < "$DEVLOG") -> $(wc -l < "$T/rtk.txt") lines"
else
  echo "(rtk not installed)"
fi

echo
echo "## 4. what the model did"
LAYA_PRUNE_HOME="$T/home" "$PY" "$S" last 5 2>/dev/null | head -n 8
ID=$(LAYA_PRUNE_HOME="$T/home" "$PY" "$S" last 5 2>/dev/null | awk '$0 ~ /%/ {print $1}' | head -1)
[ -n "$ID" ] && LAYA_PRUNE_HOME="$T/home" "$PY" "$S" show "$ID" 2>/dev/null | head -n 4

echo
echo "## 5. safety: error/warning lines in vs out (slice-compared for model)"
echo "collapse.txt: errors=$(grep -ci 'error\|critical' "$DEVLOG") in / $(grep -ci 'error\|critical' "$T/collapse.txt") out; warnings=$(grep -ci 'warning' "$DEVLOG") in / $(grep -ci 'warning' "$T/collapse.txt") out"
echo "model.txt:    errors=$(grep -ci 'error\|critical' "$T/slice.txt") in / $(grep -ci 'error\|critical' "$T/model.txt") out; warnings=$(grep -ci 'warning' "$T/slice.txt") in / $(grep -ci 'warning' "$T/model.txt") out"
[ -f "$T/rtk.txt" ] && echo "rtk.txt:      errors=$(grep -ci 'error\|critical' "$DEVLOG") in / $(grep -ci 'error\|critical' "$T/rtk.txt") out; warnings=$(grep -ci 'warning' "$DEVLOG") in / $(grep -ci 'warning' "$T/rtk.txt") out"
echo
echo "metrics for this demo only: $T/home (yours untouched)"
