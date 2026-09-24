#!/usr/bin/env bash
# install.sh - set up lx + laya-prune on this machine.
#   ./scripts/install.sh                 base install
#   ./scripts/install.sh --with all      + editor integrations
#   ./scripts/install.sh --with claude-code,copilot,codex,vscode
#   PREFIX=~/.local ./scripts/install.sh   (override bin dir)
#
# Does: venv + deps, ~/.local/bin/{lx,laya-prune}, opencode script+package
# copies, opencode plugin, agent memory file (~/.claude/LX.md).
# Safe to re-run (idempotent). Needs: python3, network for pip (laya/torch).
set -euo pipefail
REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${PREFIX:-$HOME/.local}/bin"
PYBIN="${PYTHON:-python3}"
WITH=""
while [ $# -gt 0 ]; do
  case "$1" in
    --with) WITH="${2:-}"; shift 2;;
    --with=*) WITH="${1#--with=}"; shift;;
    -h|--help) sed -n '2,12p' "$0"; echo "extra: --with claude-code,copilot,codex,vscode (or all)"; exit 0;;
    *) echo "unknown arg: $1"; exit 1;;
  esac
done

echo "==> repo: $REPO"
[ -f "$REPO/requirements.txt" ] || { echo "run from the lx repo checkout"; exit 1; }

echo "==> venv + deps"
[ -x "$REPO/.venv/bin/python" ] || "$PYBIN" -m venv "$REPO/.venv"
"$REPO/.venv/bin/pip" install -q -r "$REPO/requirements.txt"

echo "==> symlinks in $BIN"
mkdir -p "$BIN"
ln -sf "$REPO/lx" "$BIN/lx"
cat > "$BIN/laya-prune" <<EOF
#!/usr/bin/env bash
exec "$REPO/.venv/bin/python" "$REPO/laya_prune.py" "\$@"
EOF
chmod +x "$BIN/laya-prune"

echo "==> opencode files"
mkdir -p "$HOME/.config/opencode/plugins"
cp "$REPO/laya_prune.py" "$HOME/.config/opencode/laya_prune.py"
rm -rf "$HOME/.config/opencode/src/lx_prune"
mkdir -p "$HOME/.config/opencode/src"
cp -r "$REPO/src/lx_prune" "$HOME/.config/opencode/src/lx_prune"
cp "$REPO/plugins/laya-prune.ts" "$HOME/.config/opencode/plugins/laya-prune.ts"

echo "==> agent memory (~/.claude/LX.md)"
mkdir -p "$HOME/.claude"
cp "$REPO/docs/AGENT_LX.md" "$HOME/.claude/LX.md"
touch "$HOME/.claude/CLAUDE.md"
grep -q "@LX.md" "$HOME/.claude/CLAUDE.md" || echo "@LX.md" >> "$HOME/.claude/CLAUDE.md"

echo "==> verify"
export PATH="$BIN:$PATH"
command -v lx
lx status 2>&1 | head -n 4 || true
if [ -n "$WITH" ]; then
  echo "==> editor integrations ($WITH)"
  "$REPO/.venv/bin/python" "$REPO/scripts/editor_setup.py" install \
    --with "$WITH" --repo "$REPO" --bin "$BIN"
fi
echo
echo "installed. Add to PATH if needed:  export PATH=\"$BIN:\$PATH\""
echo "try:  lx git status   |   lx gain   |   lx tune"
