# Setup for VSCode, Claude Code, Codex, Copilot and friends

Base install first (works everywhere — all you strictly need is `lx` on PATH):

```bash
git clone <this-repo> && cd laya_based_noise_removal_tool
./scripts/install.sh
```

Then add the integrations you use (idempotent, re-runnable, backed up):

```bash
./scripts/install.sh --with all
# or pick: --with claude-code,copilot,codex,vscode
./scripts/uninstall.sh   # removes editor integrations too (metrics kept)
```

## What each integration does

| platform | mechanism | output filtering | instruction |
|---|---|---|---|
| OpenCode | plugin `laya-prune.ts` (base install) | ✅ every Bash call | `AGENTS.md` |
| Claude Code | `PostToolUse` hook on `Bash` in `~/.claude/settings.json` | ✅ hook rewrites output | `~/.claude/LX.md` (base install) |
| Copilot CLI | `lx` rule block in `~/.copilot/copilot-instructions.md` | via `lx` prefix | ✅ same block |
| Codex | `lx` block in `~/.codex/AGENTS.md` (read natively) | via `lx` prefix | ✅ same block |
| VSCode terminal | `~/.local/bin` prepended to terminal `PATH` | makes `lx` callable | — |
| Cursor / Gemini / Cline | repo `AGENTS.md` (all read it natively) | via `lx` prefix | ✅ `AGENTS.md` |
| Copilot VSCode extension | repo `.github/copilot-instructions.md` | via `lx` prefix | ✅ that file |

Why instruction-layer for most tools: only OpenCode (plugin) and Claude Code
(PostToolUse output mutation) offer a hook that can rewrite *output*.
Copilot CLI's hook is pre-execution rewrite only (that's how `rtk hook
copilot` works too) — so for those tools the agent itself invokes `lx`,
exactly like the established `rtk <cmd>` prefix pattern in
`~/.copilot/copilot-instructions.md`.

## Verify per platform

```bash
# Claude Code: hook present?
python3 -c "import json; print([e for e in json.load(open('$HOME/.claude/settings.json'))['hooks'].get('PostToolUse', [])])"
# Copilot CLI / Codex: block present?
grep -c lx-instructions ~/.copilot/copilot-instructions.md ~/.codex/AGENTS.md
# VSCode: PATH merged?
python3 -c "import json; print(json.load(open('$HOME/.config/Code/User/settings.json'))['terminal']['integrated']['env'])"
# Any agent, anywhere (source of truth — memory can be stale):
command -v lx >/dev/null && lx status || echo LX_NOT_INSTALLED
```

## Manual setup (no script)

- **Claude Code**: add to `~/.claude/settings.json` →
  `hooks.PostToolUse: [{matcher: "Bash", hooks: [{type: "command",
  command: "<repo>/.venv/bin/python <home>/.config/opencode/laya_prune.py"}]}]`
  (payload: Claude PostToolUse JSON on stdin; output: `hookSpecificOutput.updatedToolOutput`).
- **Copilot / Codex**: copy the block from `scripts/editor_setup.py:LX_BLOCK`
  into `~/.copilot/copilot-instructions.md` / `~/.codex/AGENTS.md`.
- **VSCode**: add `~/.local/bin` to `terminal.integrated.env.linux.PATH`.

## Uninstall awareness

`uninstall.sh` removes hook entries (matched by `laya_prune.py`), marker
blocks, and our PATH prefix — everything else untouched. Agents must still
presence-check (`command -v lx`) because a running session can hold stale
memory after uninstall.
