#!/usr/bin/env bash
# uninstall.sh - remove everything install.sh put on this machine.
#   ./scripts/uninstall.sh          keep metrics history + tune file (default)
#   ./scripts/uninstall.sh --purge  also delete metrics, samples, tune file
#
# After this, `which lx` fails and agents fall back to raw commands.
# The agent memory reference is removed too, so agents stop expecting lx.
set -euo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${PREFIX:-$HOME/.local}/bin"
PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

echo "==> symlinks"
rm -f "$BIN/lx" "$BIN/laya-prune"

echo "==> opencode files"
rm -f "$HOME/.config/opencode/laya_prune.py"
rm -rf "$HOME/.config/opencode/src/lx_prune"
rmdir "$HOME/.config/opencode/src" 2>/dev/null || true
rm -f "$HOME/.config/opencode/plugins/laya-prune.ts"
rmdir "$HOME/.config/opencode/plugins" 2>/dev/null || true

echo "==> editor integrations (all lx-owned entries/blocks)"
"$REPO/.venv/bin/python" "$REPO/scripts/editor_setup.py" uninstall --with all --repo "$REPO" 2>/dev/null \
  || python3 "$REPO/scripts/editor_setup.py" uninstall --with all --repo "$REPO" 2>/dev/null \
  || echo "(editor cleanup skipped: no python)"

echo "==> agent memory"
rm -f "$HOME/.claude/LX.md"
if [ -f "$HOME/.claude/CLAUDE.md" ]; then
  sed -i '/@LX\.md/d' "$HOME/.claude/CLAUDE.md"
fi

if [ "$PURGE" = "1" ]; then
  echo "==> purge metrics + tune file"
  rm -rf "$HOME/.local/share/laya-prune" "$HOME/.config/laya-prune"
else
  echo "==> kept: ~/.local/share/laya-prune (metrics), ~/.config/laya-prune (tune)"
fi

echo "==> verify (these should FAIL now)"
if command -v lx >/dev/null 2>&1; then echo "WARN: lx still on PATH: $(command -v lx)"; else echo "ok: lx gone"; fi
[ -f "$HOME/.config/opencode/laya_prune.py" ] && echo "WARN: deployed copy left" || echo "ok: deployed copy gone"
echo "uninstalled (repo at $REPO untouched)."
