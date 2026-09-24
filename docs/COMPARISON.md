# Laya vs RTK (and why `lx` is both)

## What each owns

- **RTK**: deterministic rewrites with exact semantics — `git status/log/diff`,
  `ls`, `grep/rg`, `find`, `docker`, `npm`, `pest/phpunit`, `ps`, `curl`.
  Instant (~15ms), no model, tracked by `rtk gain` (this history: ~130K tokens,
  ~38%). Weak on free-form logs it has no filter for.
- **Laya**: model verdicts on arbitrary text — build logs, tracebacks, mixed
  noise. Costs a load (~4s) + predict (~1s/chunk on CPU). Fail-safe keep-all.
  Weak on unique-fact listings (correctly keeps them: 0% saved on `git log`).

## Method

Same fixtures through both, isolated metrics (`scripts/compare.sh`):

| fixture | raw | rtk | laya | lx (stage) |
|---|---|---|---|---|
| `git log` 50 | 50 | 14 | 50 | 14 (`rtk`) |
| `git status` 5 | 5 | 6 | 5 | 6 (`rtk`) |
| `ls -R` 300 | 300 | 300 | 300 | 366 (`rtk+laya`, rtk adds sizes) |
| npm log 502 | 502 | 8 | 10 | 10 (`laya` pipe) |
| `ps aux` ~100 | ~100 | n/a | ~100 | 32 (`rtk`) |

Rule of thumb: RTK for commands it rewrites, Laya for everything long and
free-form. `lx` implements exactly that split, so `lx gain` ≈ both savings
added. Regenerate anytime: `bash scripts/compare.sh .venv/bin/python`.
