# Architecture

## Pipeline (`src/lx_prune/prune.py:prune`)

1. **dedupe** (`collapse.py`): runs of ≥3 identical lines → one + marker.
2. **collapse** (`collapse.py`): template-normalized groups (sha/uuid/version/
   timestamp/numbers → placeholders; `pkg-123` ≡ `pkg-#`). Groups ≥
   `COLLAPSE_MIN` (×2 in error/debug modes) keep first 2 + last 1 + marker.
   `MUST_KEEP`, blank and punctuation-only lines never collapse.
3. **short-circuit**: ≤`MIN_LINES` (120) → byte-identical passthrough.
4. **head/tail**: first 20 + last 30 lines always kept.
5. **failure windows** (`rank_clusters`): hits (`MUST_KEEP`/`SUMMARY_RE`) within
   `ctx` lines form a cluster. Cluster #1 (likely root cause) gets the full
   ±ctx window (±10 error, ±6 debug, ±4 verify, ±2 minimal); later clusters
   (cascades) get ±max(2, ctx//3).
6. **chunk + force-keep**: body → 12–25-line chunks. A chunk is forced-kept if
   it matches `MUST_KEEP`/`SUMMARY_RE` or overlaps a failure window.
7. **score** (`model.py:score_chunks`): per-family question (diff hunks /
   listing / generic), batches of 16, `answer_confidence` with
   max(probabilities) fallback (raw `confidence` is uncalibrated — the
   `typed-decisions` checkpoint ships invalid temperatures, laya warns).
   Keep if choice==A or conf < threshold. Low-conf → keep. Model exception →
   keep-all + `model: error:<Type>` + venv hint recorded.
8. **markers**: dropped stretches → `[... N lines of routine output omitted ...]`.
   Output shorter-or-equal only; else original bytes back.

## Routing (`src/lx_prune/runner.py`)

`rtk hook check "<cmd>"` dry-run (rc=0 + `rtk …` = owned; rc=1 = not).
Owned → exec rewrite; output still >120 lines → Laya second pass
(`stage=rtk+laya`), else as-is (`stage=rtk`). Not owned / rtk missing →
raw + Laya (`stage=laya`). Interactive regex → raw, inherited stdio
(`bypass`). Command executes exactly once; its exit code is `lx`'s exit code
and feeds `LAYA_PRUNE_RC` (auto mode → error).

## Thresholds (`modes.py`)

Family base (keepy 0.80 / noisy 0.50 / other 0.65) → tune-file delta
(±0.10, from `tune --apply`) → mode delta (error +0.15, debug +0.10,
verify −0.05, minimal →0.40 flat) → clamp [0.30, 0.95]. Recorded per run as
`min_conf`/`tune_delta`/`mode` for `last --verbose` and `show`.

## Metrics (`metrics.py`)

Append-only `metrics.jsonl` (capped at 5000 lines, oldest 25% rotated;
override with `LAYA_PRUNE_MAX_LOG`) + capped samples (`runs/`, 200).
Readers tolerate old records (`.get` with defaults). `audit.py` replays every
sample: any `MUST_KEEP` line lost → fail. `tune` aggregates laya-scored runs
per family (see MODES_AND_TUNING).

## Limitations (honest list)

1. **CPU latency.** Model load ~4–7s + ~1s/chunk. A 500-chunk log ≈ 10 min.
   Mitigated: heuristics resolve most runs before scoring; batches of 16.
   Real fix: a persistent daemon (load once, Unix socket) — not built yet.
2. **Model rarely fires on error-dense logs.** Every chunk with an error is
   force-kept, so `scored` is often 0 and the model adds nothing. Correct
   (safety first) but means the model only earns its keep on long *clean*
   stretches — and the tune loop starves (see `tune` coverage table).
3. **Chunk-granular force-keep is coarse.** One error line pins its whole
   12–25-line chunk. Failure windows already narrow this; line-level scoring
   inside forced chunks is future work.
4. **No streaming.** The wrapper buffers to a tempfile; output appears at exit.
   Interactive commands bypass instead of streaming filtered output.
5. **Token counts are estimates** (chars/4), not your model's tokenizer —
   don't compare 1:1 with `rtk gain`.
6. **Template collapse is syntactic.** Lines that differ in wording (not just
   numbers/hashes) never group. Semantic dedupe would need embeddings.
7. **Hook coverage is uneven.** Output rewriting exists for OpenCode (plugin)
   and Claude Code (PostToolUse). Copilot/Codex/Cursor rely on the `lx`
   prefix convention — the agent must cooperate.
