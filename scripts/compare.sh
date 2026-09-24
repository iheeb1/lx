#!/usr/bin/env bash
# compare.sh - laya vs rtk on the same fixtures.
#   ./scripts/compare.sh [python]   (default: .venv/bin/python if present, else python3)
# Uses an isolated LAYA_PRUNE_HOME so real metrics are untouched.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
if [ -n "${1:-}" ]; then PY="$1"; elif [ -x "$REPO/.venv/bin/python" ]; then PY="$REPO/.venv/bin/python"; else PY=python3; fi
S="$REPO/laya_prune.py"
LX="$REPO/lx"
T=$(mktemp -d)
export LAYA_PRUNE_HOME="$T/home"
RTK="$(command -v rtk || true)"

tok() { printf '%s' "$1" | wc -c | awk '{printf "%d", ($1+3)/4}'; }
row() { # name raw_file rtk_cmd(with $T expanded by caller) laya_label lx_cmd
  local name="$1" raw="$2" rtk_cmd="$3" label="$4" lx_cmd="$5"
  local raw_lines laya_out rtk_out lx_out stage
  raw_lines=$(wc -l < "$raw")
  if [ -n "$RTK" ] && [ -n "$rtk_cmd" ]; then
    rtk_out=$(eval "$rtk_cmd" 2>/dev/null | wc -l)
  else
    rtk_out="n/a"
  fi
  LAYA_PRUNE_CMD="$label" "$PY" "$S" --pipe < "$raw" > "$T/laya.out" 2>/dev/null
  laya_out=$(wc -l < "$T/laya.out")
  # lx runs the live command (same HOME, so `last 1` is its record)
  lx_out=$(eval "$LX $lx_cmd" 2>/dev/null | wc -l)
  stage=$("$PY" "$S" last 1 2>/dev/null | awk 'NR==2 {print $NF}')
  echo "| $name | $raw_lines | $rtk_out | $laya_out | $lx_out ($stage) |"
}

echo "# laya vs rtk comparison ($(date -u +%FT%TZ), py=$PY, rtk=${RTK:-missing})"
echo
echo "| fixture | raw lines | rtk lines | laya lines | lx lines (model) |"
echo "|---|---|---|---|---|"

# --- fixtures (small, fast, no model load dominates) ---
git -C /home/iheb/sv2c-core status --short > "$T/git-status.txt" 2>&1
git -C /home/iheb/sv2c-core log --oneline -50 > "$T/git-log.txt" 2>&1
git -C /home/iheb/sv2c-core diff --stat > "$T/git-diff.txt" 2>&1
ls -R /home/iheb/sv2c-core/src 2>/dev/null | head -n 300 > "$T/ls-r.txt"
rg --no-heading "import" /home/iheb/sv2c-core/src 2>/dev/null | head -n 200 > "$T/rg.txt"
ps aux 2>/dev/null | head -n 150 > "$T/ps.txt"
{ echo '$ npm install'
  for i in $(seq 1 300); do echo "npm http fetch GET 200 https://registry.npmjs.org/pkg-$i ${i}ms"; done
  echo "src/app.ts:42:7 - error TS2322: Type 'string' is not assignable to type 'number'."
  for i in $(seq 1 200); do echo "npm http fetch GET 200 https://registry.npmjs.org/dep-$i ${i}ms"; done
} > "$T/npm.txt"

row "git status" "$T/git-status.txt" "rtk git -C /home/iheb/sv2c-core status 2>/dev/null" "git status --short" "git -C /home/iheb/sv2c-core status"
row "git log" "$T/git-log.txt" "rtk git -C /home/iheb/sv2c-core log 2>/dev/null | head -n 200" "git log --oneline -50" "git -C /home/iheb/sv2c-core log --oneline -50"
row "ls -R" "$T/ls-r.txt" "rtk ls -R /home/iheb/sv2c-core/src 2>/dev/null | head -n 300" "ls -R src" "ls -R /home/iheb/sv2c-core/src"
row "ps aux" "$T/ps.txt" "" "ps aux" "ps aux"
echo
echo "npm fixture (pipe mode): raw $(wc -l < "$T/npm.txt") -> laya $(LAYA_PRUNE_CMD='npm install' "$PY" "$S" --pipe < "$T/npm.txt" 2>/dev/null | wc -l), rtk log $(rtk log "$T/npm.txt" 2>/dev/null | wc -l), lx pipe $(LAYA_PRUNE_CMD='npm install' "$LX" pipe < "$T/npm.txt" 2>/dev/null | wc -l)"
echo
echo "_Lower lines = more saved. rtk-owned rows should show lx model rtk-passthrough._"
echo "_Detail per run: LAYA_PRUNE_HOME is isolated. For real totals see: laya-prune stats / rtk gain._"
