#!/usr/bin/env bash
# Smoke test for laya_prune.py (integration: loads the real Laya model).
#   ./tests/smoke_test.sh [python]              full test (e.g. .venv/bin/python)
#   LAYA_PRUNE_NO_MODEL=1 ./tests/smoke_test.sh plumbing only, no model
set -u
PY=${1:-python3}
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/.." && pwd)
S="$REPO/laya_prune.py"
LX="$REPO/lx"
AUDIT="$REPO/scripts/audit.py"
T=$(mktemp -d)
export LAYA_PRUNE_HOME="$T/home"
pass=0; fail=0
check() { if eval "$2"; then echo "PASS  $1"; pass=$((pass+1)); else echo "FAIL  $1"; fail=$((fail+1)); fi; }

# ---- fixtures: a noisy build log with one real error buried in the middle
{
  echo '$ npm install'
  for i in $(seq 1 300); do echo "npm http fetch GET 200 https://registry.npmjs.org/pkg-$i ${i}ms"; done
  echo "src/app.ts:42:7 - error TS2322: Type 'string' is not assignable to type 'number'."
  for i in $(seq 1 200); do echo "npm http fetch GET 200 https://registry.npmjs.org/dep-$i ${i}ms"; done
  for i in $(seq 1 30); do echo "added $i packages"; done
} > "$T/big.log"
seq 1 30 > "$T/small.log"

# ---- 1. short output passes through untouched
check "short output unchanged" "$PY $S --pipe < $T/small.log | cmp -s - $T/small.log"

# ---- 2. long output: the buried error, the head and the tail survive
$PY "$S" --pipe < "$T/big.log" > "$T/big.out" 2> "$T/big.err"
check "error line kept"        "grep -q TS2322 $T/big.out"
check "head kept"              "head -1 $T/big.out | grep -q 'npm install'"
check "tail kept"              "tail -1 $T/big.out | grep -q 'added 30 packages'"
check "output shrank"          "[ \$(wc -l < $T/big.out) -lt \$(wc -l < $T/big.log) ]"

# ---- 2b. model-free collapse alone (no Laya) must already shrink it and keep the error
LAYA_PRUNE_NO_MODEL=1 $PY "$S" --pipe < "$T/big.log" > "$T/collapse.out"
check "collapse alone shrinks + keeps error" \
  "[ \$(wc -l < $T/collapse.out) -lt 50 ] && grep -q TS2322 $T/collapse.out"

# ---- 2c. Laya alone (collapse disabled) must drop noisy npm chunks.
# Command-aware threshold: "npm install" -> 0.50 so B@0.52 drops; without a
# label the default is safer (0.65) and may keep more.
if [ "${LAYA_PRUNE_NO_MODEL:-0}" != "1" ]; then
  LAYA_PRUNE_CMD="npm install" LAYA_PRUNE_NO_COLLAPSE=1 $PY "$S" --pipe < "$T/big.log" > "$T/model_only.out" 2>/dev/null
  echo "INFO  Laya alone: $(wc -l < $T/big.log) -> $(wc -l < $T/model_only.out) lines"
  check "model alone drops noise but keeps error" \
    "[ \$(wc -l < $T/model_only.out) -lt \$(wc -l < $T/big.log) ] && grep -q TS2322 $T/model_only.out"
fi

# ---- 3. metrics recorded and viewable
check "stats works"            "$PY $S stats | grep -q 'commands seen'"
check "last works"             "$PY $S last | grep -q 'tok in'"
ID=$($PY "$S" last 5 | awk '$0 ~ /%/ {print $1}' | head -1)
if [ -n "$ID" ]; then
  check "show --diff works"    "$PY $S show $ID --diff | grep -q '^--- original'"
fi

# ---- 4. the exact wrapper the OpenCode plugin builds: exit code must survive
CMD='cat '"$T"'/big.log; exit 3'
cat > "$T/wrapped.sh" <<EOF
__lp=\$(mktemp)
(
$CMD
) >"\$__lp" 2>&1
__rc=\$?
LAYA_PRUNE_CMD='smoke wrapper' '$PY' '$S' --pipe <"\$__lp" || cat "\$__lp"
rm -f "\$__lp"
exit \$__rc
EOF
bash "$T/wrapped.sh" > "$T/wrapped.out"; RC=$?
check "wrapper keeps exit code (3)" "[ $RC -eq 3 ]"
check "wrapper keeps error line"    "grep -q TS2322 $T/wrapped.out"

# ---- 5. hook-mode JSON
"$PY" - "$T/big.log" > "$T/payload.json" <<'EOF'
import json, sys
print(json.dumps({"hook_event_name": "PostToolUse", "tool_name": "Bash",
                  "tool_input": {"command": "npm install"},
                  "tool_response": {"stdout": open(sys.argv[1]).read(), "stderr": ""}}))
EOF
$PY "$S" < "$T/payload.json" > "$T/hook.out"
check "hook output is empty or valid JSON" \
  "[ ! -s $T/hook.out ] || $PY -c 'import json,sys; json.load(open(\"$T/hook.out\"))'"

# ---- 6. model failure must fail SAFE (keep everything), not lose output
mkdir "$T/broken"; printf 'def load(*a, **k):\n    raise RuntimeError("boom")\n' > "$T/broken/laya.py"
env -u LAYA_PRUNE_NO_MODEL LAYA_PRUNE_NO_COLLAPSE=1 PYTHONPATH="$T/broken" $PY "$S" --pipe < "$T/big.log" > "$T/fallback.out" 2>/dev/null
check "broken model keeps all output" "cmp -s $T/fallback.out $T/big.log"

# ---- 7. observability: doctor, status, stats --verbose, last --failed
check "doctor works"             "$PY $S doctor | grep -q 'python:'"
check "status works"             "$PY $S status | grep -q 'runs:'"
check "watch explains + exits 2" "$PY $S watch 2>&1 | grep -q 'no .watch. subcommand'; [ \$? -eq 0 ]"
check "unknown cmd exits 2, no hang" "echo hi | $PY $S fgsfds-nope 2>&1 | grep -q 'unknown command'"
check "stats --verbose works"    "$PY $S stats --verbose | grep -q 'thresholds'"
check "last --failed works"      "$PY $S last 5 --failed | grep -q 'id'"
check "audit keeps must-keep lines" "$PY $AUDIT | grep -q 'checked'"

# ---- 8. lx unified wrapper (isolated HOME so real metrics stay clean)
export LAYA_PRUNE_HOME="$T/lx-home"
check "lx routes rtk-owned cmd"  "$LX git -C $REPO status >/dev/null 2>&1; $PY $S last 1 2>/dev/null | grep -q 'rtk'"
check "lx laya branch keeps output" "[ \"\$($LX echo lx-hello)\" = 'lx-hello' ]"
check "lx preserves exit code"   "$LX sh -c 'exit 7' >/dev/null 2>&1; [ \$? -eq 7 ]"
check "lx bypass runs raw"       "$LX watch --help 2>&1 | grep -qi 'usage'"
check "lx laya-only fallback"    "LX_NO_RTK=1 $LX echo lx-fallback | grep -q lx-fallback"
check "lx gain merges both"      "$LX gain 2>/dev/null | grep -q 'commands seen'"
unset LAYA_PRUNE_HOME
export LAYA_PRUNE_HOME="$T/home"

# ---- 9. context modes: thresholds shift, error window keeps neighborhoods
cat > "$T/mode_probe.py" <<'EOF'
import os, sys
sys.path.insert(0, os.environ.get("PROBE_DIR", "."))
import laya_prune as lp
print(lp.effective_min_conf(sys.argv[1]))
EOF
check "error mode raises bar" \
  "PROBE_DIR="$REPO" LAYA_PRUNE_MODE=error $PY $T/mode_probe.py pytest | grep -q '0.65'"
check "auto+rc1 behaves like error" \
  "PROBE_DIR="$REPO" LAYA_PRUNE_MODE=auto LAYA_PRUNE_RC=1 $PY $T/mode_probe.py pytest | grep -q '0.65'"
check "minimal mode drops bar" \
  "PROBE_DIR="$REPO" LAYA_PRUNE_MODE=minimal $PY $T/mode_probe.py ls | grep -q '0.4'"
printf 'line1\nline2\nsetup testdb://x\nFAILED boom\nline5\n' > "$T/err.log"
check "error window keeps setup lines" \
  "LAYA_PRUNE_CMD='pytest' LAYA_PRUNE_MODE=error LAYA_PRUNE_NO_COLLAPSE=1 LAYA_PRUNE_MIN_LINES=1 $PY $S --pipe < $T/err.log 2>/dev/null | grep -q 'testdb'"
check "lx sets rc for auto mode" \
  "LAYA_PRUNE_HOME='$T/lxrc-home' $LX sh -c 'exit 2' >/dev/null 2>&1; $PY -c 'import json,glob,os; recs=[json.loads(l) for l in open(sorted(glob.glob(os.path.expandvars(\"$T/lxrc-home/metrics.jsonl\")))[0])]; print(recs[-1].get(\"mode\"))' | grep -q error"

# ---- 10. failure clusters + learned thresholds (isolated files)
cat > "$T/cluster_probe.py" <<'EOF'
import os, sys
sys.path.insert(0, os.environ.get("PROBE_DIR", "."))
import laya_prune as lp
lines = ["ok %d" % i for i in range(60)]
lines[10] = "FAILED first boom"
lines[50] = "FAILED cascade boom"
keep, info = lp.ranked_windows(lines, 9)
assert info["clusters"] == 2, info
assert 10 in keep and 1 in keep and 19 in keep, "root full window"
assert 40 not in keep, "gap between clusters dropped"
assert 47 in keep and 53 in keep and 46 not in keep and 54 not in keep, "cascade shrunk window"
print("clusters ok:", info)
EOF
check "cluster ranking shrinks cascades" "PROBE_DIR="$REPO" $PY $T/cluster_probe.py | grep -q 'clusters ok'"
export LAYA_PRUNE_HOME="$T/tune-home" LAYA_PRUNE_TUNE_FILE="$T/tune.json"
mkdir -p "$T/tune-home"
$PY -c 'import json; [print(json.dumps({"id": "s%d" % i, "ts": "2026-09-24T00:00:00", "cmd": "npm seed", "stage": "laya", "lines_in": 500, "lines_out": 490, "tok_in": 2000, "tok_out": 1960, "changed": True, "chunks": 20, "forced": 0, "scored": 20, "dropped_chunks": 0, "collapsed": 0, "model": "laya", "ms": 1, "verdicts": {"A": 20, "B": 0, "low_conf": 0}})) for i in range(4)]' > "$T/tune-home/metrics.jsonl"
check "tune suggests -0.05 for timid family" "$PY $S tune 2>/dev/null | grep -q -- '-0.05'"
check "tune --apply writes file" "$PY $S tune --apply 2>/dev/null | grep -q 'wrote'; grep -q 'noisy' $T/tune.json"
check "tuned delta shifts threshold" "[ \"\$(PROBE_DIR="$REPO" $PY $T/mode_probe.py 'npm seed')\" = '0.45' ]"
unset LAYA_PRUNE_TUNE_FILE
export LAYA_PRUNE_HOME="$T/home"

echo; echo "passed: $pass  failed: $fail   (fixtures in $T)"
[ "$fail" -eq 0 ]
