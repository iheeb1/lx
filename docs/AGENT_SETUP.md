# Agent setup (and uninstall awareness)

## What install.sh wires into the agent

1. `~/.claude/LX.md` — memory file with the working contract (copied from
   `docs/AGENT_LX.md`), referenced from `~/.claude/CLAUDE.md` via `@LX.md`
   (same pattern as `@RTK.md`).
2. OpenCode plugin `laya-prune.ts` — routes bash output through `lx`.
3. `~/.local/bin/{lx,laya-prune}` + `~/.config/opencode/{laya_prune.py,src/,plugins/}`.

## Presence check (agents: run this before assuming lx exists)

```bash
command -v lx >/dev/null && lx status || echo "LX_NOT_INSTALLED"
```

- `LX_NOT_INSTALLED` → use raw commands (and `rtk` if present). Do NOT claim
  savings, do NOT reference `lx` flags.
- After `scripts/uninstall.sh`, the `@LX.md` line is removed from CLAUDE.md
  and `LX.md` deleted — but a running session may hold stale context, so the
  presence check above is the source of truth, not memory.

## Rules for agents using lx

- Prefer `lx <cmd>` over bare commands for anything that can exceed ~100 lines.
- Never wrap `watch`, `tail -f`, `less`, `top`, `ssh`, dev servers — run raw.
  (`lx` bypasses them anyway; the plugin skips wrapping.)
- Exit codes are the command's own — trust `rc != 0` as failure.
- On failure output, `lx` already kept error neighborhoods (auto→error mode);
  don't re-run with `MODE=error` unless output was over-pruned.
- `lx tune` is read-only; `tune --apply` changes behavior — ask the user first.
- `lx reset --purge`... doesn't exist: `laya_prune.py reset` wipes metrics.
  Never run destructive subcommands (`reset`, `uninstall --purge`) unasked.
