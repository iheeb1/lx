# lx — unified shell-output pruning (Laya + RTK)

Long command output eats agent context. `lx` routes every command through the
cheapest filter that is safe, and records what it did so thresholds learn from
your own history.

- **RTK** (deterministic rewrites: `git status`, `ls`, `grep`…) — instant, exact.
- **Laya** (`convaiinnovations/laya`, `typed-decisions`) — model verdicts on the
  long tail: build logs, test output, mixed noise. Fail-safe: any doubt or any
  model error keeps everything.
- **Stages** per run: `rtk` → `rtk+laya` (second pass when RTK output stays
  long) → `laya` → `raw`/`bypass`. Exit codes always preserved.

## Install / uninstall

```bash
git clone <this-repo> && cd laya_based_noise_removal_tool
./scripts/install.sh                 # base: venv, lx on PATH, opencode, memory
./scripts/install.sh --with all      # + Claude Code hook, Copilot, Codex, VSCode
./scripts/uninstall.sh        # removes all of the above (keeps metrics)
./scripts/uninstall.sh --purge  # also deletes metrics + tune file
```

Editors/agents matrix (VSCode, Claude Code, Codex, Copilot…):
`docs/SETUP_EDITORS.md`.

After uninstall `which lx` fails on purpose — agents detect that and fall back
to raw commands (see `docs/AGENT_SETUP.md`).

## Usage

```bash
lx git status                  # RTK-owned: instant compact output
lx npm test                    # noisy log: collapse + model second pass
lx --help                      # full help
lx gain                        # rtk gain + laya stats side by side
lx status                      # one-screen health
lx tune                        # learn threshold deltas from YOUR history (dry run)
lx tune --apply                # write ~/.config/laya-prune/thresholds.json
LAYA_PRUNE_MODE=debug lx pytest -x   # keep INFO/DEBUG neighborhoods
LAYA_PRUNE_MODE=verify lx npm test   # keep the verdict, drop the chatter
```

Modes (`auto` default; `lx` feeds the real exit code so `auto` → `error` on
failure): `error` (+0.15, full failure windows), `debug` (+0.10, keeps info
logs), `verify` (−0.05, keeps summaries), `minimal` (0.40 flat token diet).
Details: `docs/MODES_AND_TUNING.md`.

## Layout

```
src/lx_prune/   config patterns modes collapse model prune metrics cli runner
laya_prune.py   thin shim (plugin + `import laya_prune` compat)
lx              thin shim (venv re-exec + runner)
plugins/        opencode plugin (calls lx; legacy inline fallback)
scripts/        install.sh uninstall.sh audit.py compare.sh
tests/          unit (unittest, no model) + smoke_test.sh (integration, model)
docs/           ARCHITECTURE COMPARISON MODES_AND_TUNING AGENT_SETUP AGENT_LX
```

## Tests

```bash
.venv/bin/python -m unittest discover -s tests -t .   # fast, no model (~0.01s)
bash tests/smoke_test.sh .venv/bin/python             # full, loads Laya (~2-4 min)
.venv/bin/python scripts/audit.py                     # must-keep lines never dropped?
bash scripts/compare.sh .venv/bin/python              # laya vs rtk table
```

## Troubleshooting

| symptom | cause → fix |
|---|---|
| `model failures … keep-everything`, `ModuleNotFoundError` | system python lacks laya → `lx` re-execs into `.venv`; raw `python3 laya_prune.py` needs `LAYA_PRUNE_PY=.venv/bin/python` |
| model keeps 100% | raw `confidence` is uncalibrated (~0.01); we use `answer_confidence` — see `docs/ARCHITECTURE.md` |
| `watch`/`tail -f` black screen | never wrap interactive commands; `lx` and the plugin bypass them — run raw |
| `laya-prune: command not found` | re-run `scripts/install.sh` (creates `~/.local/bin/lx`) |

## How it compares

| fixture (lines) | rtk | laya | lx |
|---|---|---|---|
| `git log` 50 | 14 | 50 (kept: unique facts) | 14+ via `rtk` stage |
| npm log 502 | 8 | 10 | 10 via `laya` stage |
| `git status` 5 | 6 | 5 | `rtk` stage |

Full method: `docs/COMPARISON.md`, regenerate: `scripts/compare.sh`.
