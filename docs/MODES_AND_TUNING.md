# Modes and tuning

## Context modes (`LAYA_PRUNE_MODE`, default `auto`)

What the agent needs depends on *why* it ran the command. `lx` sets
`LAYA_PRUNE_RC` from the real exit code, so `auto` behaves as `error` on
failure and default on success — no flags needed in the common case.

| mode | threshold | collapse | window | when |
|---|---|---|---|---|
| `auto` | family base | ×1 | 8 | default |
| `error` | +0.15 | ×2 | 10 | failure triage |
| `debug` | +0.10 | ×2 | 6 | active debugging, keep INFO/DEBUG |
| `verify` | −0.05 | ×1 | 4 | green-run check, keep summaries |
| `minimal` | →0.40 | ×1 | 2 | token diet |

`SUMMARY_RE` (`3 failed, 307 passed`, `BUILD SUCCESS`, coverage…) is
force-kept in every mode, so `verify` drops chatter but never the verdict.
Override per invocation (`LAYA_PRUNE_MODE=debug lx pytest`) or per session
(`export`). `LAYA_PRUNE_FIXED_CONF=1` freezes the base for experiments.

## Failure clusters

Failures arrive in bursts (one root cause + cascades). Hits within `ctx`
lines cluster together; the first cluster keeps its full window, later ones
keep ±max(2, ctx//3). Recorded as `clusters`/`root_cause` — `show <id>`
prints the root line. Tune the radius with `LAYA_PRUNE_ERR_CTX`.

## Learning loop (`tune`)

```bash
lx tune           # dry run: drop%/saved%/unsure% per family + suggestion
lx tune --apply   # merge into ~/.config/laya-prune/thresholds.json (±0.10 cap)
```

Rules: drop-rate <10% with saved <10% → −0.05 (too timid); unsure >50% →
+0.05 (guessing, be safer); else 0. Needs ≥3 model-scored runs per family —
with mostly short commands this takes days of real use, which is correct:
no data, no change. After applying, re-run `scripts/audit.py`; if a delta
ever costs a must-keep line, revert that family to 0.0 and tell the model
less, not more.
