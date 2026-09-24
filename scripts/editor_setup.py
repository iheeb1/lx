#!/usr/bin/env python3
"""Editor/agent integrations for lx. Idempotent, marker-based, no clobbering.

Usage:
  editor_setup.py install   --with claude-code,copilot,codex,vscode [--home DIR] [--repo DIR] [--bin DIR]
  editor_setup.py uninstall [--with ...] [--home DIR] [--repo DIR] [--bin DIR]

What each feature does:
  claude-code  PostToolUse hook on Bash in ~/.claude/settings.json that pipes
               output through laya_prune.py hook mode (same shape opencode uses).
  copilot      appends an `lx` rule block to ~/.copilot/copilot-instructions.md
               (same file/pattern rtk uses on this machine).
  codex        appends an `lx` block to ~/.codex/AGENTS.md (read natively).
  vscode       prepends the bin dir to terminal PATH in
               ~/.config/Code/User/settings.json (with timestamped backup).

Uninstall removes exactly what install added (hook entries referencing
laya_prune.py, marker blocks, our PATH prefix) and leaves everything else.
Respects $HOME (sandboxable). Exits nonzero with a message on JSON errors.
"""

import argparse
import json
import shutil
import sys
from datetime import datetime
from pathlib import Path

MARK_BEGIN = "<!-- lx-instructions v1 -->"
MARK_END = "<!-- /lx-instructions -->"

LX_BLOCK = """<!-- lx-instructions v1 -->
# LX — pruned shell output (Laya + RTK)

**lx** routes shell commands through the cheapest safe filter (RTK rewrites
for `git/ls/grep`, Laya model for noisy logs). Failures and error
neighborhoods are always kept; exit codes are the command's own.

## Rule

Prefer `lx` for output that can exceed ~100 lines:

```bash
# Instead of:              Use:
npm test                   lx npm test
git log -20                lx git log -20
ls -R src                  lx ls -R src
ps aux                     lx ps aux
```

## Never filter

`watch`, `tail -f`, `less`, `top`, `ssh`, dev servers — run raw
(buffering a never-ending command shows a black screen).

## Meta

```bash
lx gain                     # savings: rtk gain + laya stats
lx status                   # health check (if missing: tool uninstalled, use raw)
```
<!-- /lx-instructions -->
"""


def _load_json(path):
    try:
        return json.loads(Path(path).read_text())
    except FileNotFoundError:
        return {}
    except json.JSONDecodeError as exc:
        sys.exit(f"refusing to touch invalid JSON {path}: {exc}")


def _backup(path):
    p = Path(path)
    if p.exists():
        bak = p.with_suffix(p.suffix + f".bak-{datetime.now():%Y%m%d%H%M%S}")
        shutil.copy2(p, bak)
        return bak
    return None


def hook_command(home, repo):
    """Command string Claude Code runs per Bash tool use (JSON on stdin)."""
    venv_py = Path(repo) / ".venv" / "bin" / "python"
    script = Path(home) / ".config" / "opencode" / "laya_prune.py"
    return f"{venv_py} {script}"


# ---------------------------------------------------------------- claude-code

def install_claude_code(home, repo, bin_dir):
    del bin_dir
    settings = Path(home) / ".claude" / "settings.json"
    data = _load_json(settings)
    hooks = data.setdefault("hooks", {}).setdefault("PostToolUse", [])
    cmd = hook_command(home, repo)
    if any(h.get("command") == cmd for e in hooks for h in e.get("hooks", [])):
        return "claude-code: hook already present"
    hooks.append({"matcher": "Bash", "hooks": [{"type": "command", "command": cmd}]})
    settings.parent.mkdir(parents=True, exist_ok=True)
    _backup(settings)
    settings.write_text(json.dumps(data, indent=2) + "\n")
    return "claude-code: PostToolUse hook installed"


def uninstall_claude_code(home, repo, bin_dir):
    del repo, bin_dir
    settings = Path(home) / ".claude" / "settings.json"
    data = _load_json(settings)
    hooks = (data.get("hooks") or {}).get("PostToolUse", [])
    kept = []
    removed = 0
    for entry in hooks:
        sub = [h for h in entry.get("hooks", []) if "laya_prune.py" not in h.get("command", "")]
        removed += len(entry.get("hooks", [])) - len(sub)
        if sub:
            kept.append({**entry, "hooks": sub})
    if removed:
        data["hooks"]["PostToolUse"] = kept
        _backup(settings)
        settings.write_text(json.dumps(data, indent=2) + "\n")
    return f"claude-code: removed {removed} hook(s)"


# ------------------------------------------------------- marked-block files

def _append_block(path, block=LX_BLOCK):
    p = Path(path)
    if p.exists() and MARK_BEGIN in p.read_text():
        return f"{path}: block already present"
    p.parent.mkdir(parents=True, exist_ok=True)
    _backup(path)
    with open(p, "a") as f:
        if p.exists() and p.stat().st_size and not p.read_text().endswith("\n"):
            f.write("\n")
        f.write(block if block.endswith("\n") else block + "\n")
    return f"{path}: block appended"


def _remove_block(path):
    p = Path(path)
    if not p.exists() or MARK_BEGIN not in p.read_text():
        return f"{path}: nothing to remove"
    lines = p.read_text().splitlines(keepends=True)
    out, skipping = [], False
    for line in lines:
        if MARK_BEGIN in line:
            skipping = True
            continue
        if MARK_END in line:
            skipping = False
            continue
        if not skipping:
            out.append(line)
    _backup(path)
    p.write_text("".join(out).rstrip("\n") + "\n" if out else "")
    return f"{path}: block removed"


def install_copilot(home, repo, bin_dir):
    del repo, bin_dir
    return _append_block(Path(home) / ".copilot" / "copilot-instructions.md")


def uninstall_copilot(home, repo, bin_dir):
    del repo, bin_dir
    return _remove_block(Path(home) / ".copilot" / "copilot-instructions.md")


def install_codex(home, repo, bin_dir):
    del repo, bin_dir
    return _append_block(Path(home) / ".codex" / "AGENTS.md")


def uninstall_codex(home, repo, bin_dir):
    del repo, bin_dir
    return _remove_block(Path(home) / ".codex" / "AGENTS.md")


# ---------------------------------------------------------------- vscode

def install_vscode(home, repo, bin_dir):
    del repo
    settings = Path(home) / ".config" / "Code" / "User" / "settings.json"
    data = _load_json(settings)
    term = data.setdefault("terminal", {}).setdefault("integrated", {}).setdefault("env", {})
    linux = term.setdefault("linux", {})
    cur = linux.get("PATH", "${env:PATH}")
    if str(bin_dir) in cur:
        return "vscode: PATH already present"
    linux["PATH"] = f"{bin_dir}:${{env:PATH}}" if cur == "${env:PATH}" else f"{bin_dir}:{cur}"
    settings.parent.mkdir(parents=True, exist_ok=True)
    _backup(settings)
    settings.write_text(json.dumps(data, indent=2) + "\n")
    return "vscode: terminal PATH updated (backup kept)"


def uninstall_vscode(home, repo, bin_dir):
    del repo
    settings = Path(home) / ".config" / "Code" / "User" / "settings.json"
    data = _load_json(settings)
    try:
        cur = data["terminal"]["integrated"]["env"]["linux"]["PATH"]
    except KeyError:
        return "vscode: nothing to remove"
    # NB: never split on ":" — the ${env:PATH} placeholder contains one.
    nb = str(bin_dir)
    new = cur.replace(nb + ":", "").replace(":" + nb, "")
    if new == nb:
        new = ""
    new = new.strip(":")
    if "${env:PATH}" not in new:
        new = (new + ":${env:PATH}") if new else "${env:PATH}"
    if new == cur:
        return "vscode: nothing to remove"
    data["terminal"]["integrated"]["env"]["linux"]["PATH"] = new
    _backup(settings)
    settings.write_text(json.dumps(data, indent=2) + "\n")
    return "vscode: PATH prefix removed"


FEATURES = {
    "claude-code": (install_claude_code, uninstall_claude_code),
    "copilot": (install_copilot, uninstall_copilot),
    "codex": (install_codex, uninstall_codex),
    "vscode": (install_vscode, uninstall_vscode),
}


def parse_with(spec):
    if not spec or spec.strip().lower() in ("all", "*"):
        return sorted(FEATURES)
    out = []
    for name in spec.split(","):
        name = name.strip().lower().replace("_", "-")
        if name not in FEATURES:
            sys.exit(f"unknown feature {name!r} (choose from: {', '.join(sorted(FEATURES))})")
        out.append(name)
    return out


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("action", choices=["install", "uninstall"])
    ap.add_argument("--with", default="", help="comma list: claude-code,copilot,codex,vscode (or all)")
    ap.add_argument("--home", default=str(Path.home()))
    ap.add_argument("--repo", default=str(Path(__file__).resolve().parent.parent))
    ap.add_argument("--bin", default="")
    args = ap.parse_args(argv)
    feats = parse_with(getattr(args, "with"))
    bin_dir = args.bin or str(Path(args.home) / ".local" / "bin")
    for feat in feats:
        fn = FEATURES[feat][0 if args.action == "install" else 1]
        print(fn(args.home, args.repo, bin_dir))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
