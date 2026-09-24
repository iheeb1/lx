# LX - unified shell-output pruning (Laya + RTK)

**Usage**: run noisy commands through `lx` (cuts routine output before it
reaches context; failures, summaries and error neighborhoods are always kept).

## Meta commands (run `lx` directly)

```bash
lx <command>            # route: rtk rewrite if owned, else Laya prune
lx gain                 # token savings: rtk gain + laya stats
lx status               # health: python/laya ok?, runs, last failure
lx tune                 # threshold suggestions from history (read-only)
```

## Presence check (source of truth — memory can be stale)

```bash
command -v lx >/dev/null && lx status || echo "LX_NOT_INSTALLED"
```

If `LX_NOT_INSTALLED`: use raw commands. The tool was uninstalled
(`scripts/uninstall.sh` removes binaries, plugin, and this file's reference).

## Rules

- Prefer `lx` for output that can exceed ~100 lines (`npm/pip/docker/test`,
  `git log/diff`, `ls -R`, `ps`, build logs).
- NEVER route `watch`, `tail -f`, `less`, `top`, `ssh`, dev servers through
  any filter — run them raw (buffering would black-screen).
- Exit code of `lx <cmd>` IS the command's exit code.
- `lx tune --apply`, `laya_prune.py reset`, `uninstall --purge` are
  behavior-destroying: ask the user first, never run unasked.
- Context modes: default `auto` follows the exit code; set
  `LAYA_PRUNE_MODE=debug|verify|minimal` only when the default over/under
  prunes for the task at hand.
