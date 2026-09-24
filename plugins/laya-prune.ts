// ~/.config/opencode/plugins/laya-prune.ts   (or <project>/.opencode/plugins/laya-prune.ts)
//
// Routes each bash command through the unified `lx` tool (RTK rewrite when it
// owns the command, Laya prune otherwise). The exit code of the original
// command is preserved (lx exits with it).
//
// Fallback: if `lx` is missing, uses the legacy inline pipe through
// laya_prune.py --pipe. Interactive commands (watch, tail -f, ...) are never
// wrapped: the pipe buffers until exit, so wrapping them shows a black screen.

import * as fs from "node:fs";

const LX = `${process.env.HOME}/laya_based_noise_removal_tool/lx`;
const VENV_PY = `${process.env.HOME}/laya_based_noise_removal_tool/.venv/bin/python`;
const PY = process.env.LAYA_PRUNE_PY
  ?? (fs.existsSync(VENV_PY) ? VENV_PY : "python3");
const SCRIPT =
  process.env.LAYA_PRUNE_SCRIPT ?? `${process.env.HOME}/.config/opencode/laya_prune.py`;

// Never wrap interactive / never-ending commands (mirrors INTERACTIVE_RE in
// src/lx_prune/patterns.py — keep the two in sync).
const BYPASS = /^\s*(watch\b|tail\s+(-f|--follow)\b|tailf\b|less\b|more\b|top\b|htop\b|ssh\b|npm\s+run\s+dev|symfony\s+serve|php\s+-S|docker\s+logs\b.*?(?:-f|--follow)\b|kubectl\s+logs\b.*?(?:-f|--follow)\b|journalctl\b.*?(?:-f|--follow)\b)/;

const q = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`

export const LayaPrune = async () => {
  return {
    "tool.execute.before": async (input: any, output: any) => {
      if (input.tool !== "bash") return
      const cmd = output.args?.command
      if (typeof cmd !== "string" || cmd.includes("laya_prune.py") || cmd.includes("laya_based_noise_removal_tool/lx")) return
      if (BYPASS.test(cmd)) return

      // Preferred: single unified entry point (routes RTK vs Laya itself).
      if (!process.env.LAYA_PRUNE_NO_LX && fs.existsSync(LX)) {
        output.args.command = `${q(LX)} ${q(cmd)}`
        return
      }

      // Legacy fallback: inline temp-file pipe through laya_prune.py.
      output.args.command = [
        `__lp=$(mktemp)`,
        `(`,   // subshell: an 'exit' inside the user's command can't skip the pruner
        cmd,
        `) >"$__lp" 2>&1`,
        `__rc=$?`,
        // if the pruner itself fails, fall back to the raw output
        `LAYA_PRUNE_CMD=${q(cmd)} ${q(PY)} ${q(SCRIPT)} --pipe <"$__lp" || cat "$__lp"`,
        `rm -f "$__lp"`,
        `exit $__rc`,
      ].join("\n")
    },
  }
}
