# AGENTS.md — working in this repo

## What this is

`lx`: route shell commands through RTK (deterministic) or Laya (model),
keeping failures and error neighborhoods. Source: `src/lx_prune/`
(`config patterns modes collapse model prune metrics cli runner`).
Thin shims at root: `lx`, `laya_prune.py` (keep them thin — no logic).

## Commands

```bash
.venv/bin/python -m unittest discover -s tests -t .  # unit, no model, ~0.01s
bash tests/smoke_test.sh .venv/bin/python            # integration, real model
.venv/bin/python scripts/audit.py                    # safety gate
bash scripts/compare.sh .venv/bin/python             # laya vs rtk table
./scripts/install.sh | ./scripts/uninstall.sh [--purge]
./scripts/install.sh --with all                      # editor integrations
```

## Conventions

- No new runtime deps (stdlib only; `laya` comes from requirements.txt).
- Metrics schema is append-only: new keys ok, never rename/remove; readers
  must `.get()` with defaults (old records lack new fields).
- `audit.py` must stay green: no must-keep line ever dropped in samples.
- Threshold changes: prefer data (`tune`) over hand-tuning; update
  `docs/MODES_AND_TUNING.md` when semantics change.
- Deployed copies (`~/.config/opencode/…`, `~/.local/bin/lx`) are generated
  by `install.sh` — never edit them directly; sync via install/reinstall.
- Keep `docs/AGENT_LX.md` in sync with CLI flags; it ships to agent memory.
