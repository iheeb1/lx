# Context-aware views

Under a coding agent, lx reads the end of the current session's transcript and uses four things it finds there: what the agent is looking at (focus), how full its context is (pressure), why it ran the command (mode), and whether an earlier run of the same command is still in its context (delta). This page lists exactly what lx reads and how each signal changes a view. The README's [Context-aware](../README.md#context-aware) section is the short version.

## Finding the session

lx looks for a session only when one of these is set in its environment. Coding agents set them in the shell they run commands in:

| Agent | Variable | Transcript |
|---|---|---|
| Claude Code | `CLAUDE_CODE_SESSION_ID` | `$CLAUDE_CONFIG_DIR/projects/<slug>/<id>.jsonl` (default `~/.claude`). `<slug>` is the project directory with every character other than a letter or digit replaced by `-`. lx tries the current directory and its parents, then the other project directories (at most 512) |
| Codex | `CODEX_THREAD_ID` | `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-…-<id>.jsonl` (default `~/.codex`), found through the date in the id, then the 45 newest day directories |

A session id with anything but letters, digits, `-` and `_` is ignored. `LX_CONTEXT=0` (or `off`, `false`, `no`) stops the lookup before it starts.

**Subagents.** Claude Code keeps a subagent's transcript under `<id>/subagents/`. To find out who ran the command, lx reads the last 64 KiB of the main transcript and of up to 16 subagent transcripts changed in the last 5 minutes, and looks for a Bash call that hasn't returned yet and whose command contains lx's command. If exactly one transcript has it, that one is read and the run is *confirmed*. Otherwise lx reads the first match, or the main transcript, and treats every earlier run as out of context.

**Reading.** lx reads at most the last 1 MiB, starting at a line boundary, after the command has exited. Lines it can't parse are skipped. A transcript untouched for an hour that doesn't show the command is ignored. Any failure (no file, no permission, a directory, garbage) means no context: the run gets lx's usual view.

**When.** Only for outputs over 150 tokens, the ones lx may condense, or longer than a `--fit`. `lx pipe` uses the same context, without the delta. `lx -r`, `LX_RAW=1` and `LX_OFF=1` read nothing.

## What lx takes from a transcript

Claude Code:

- `assistant` entries: `message.model`; `message.usage` (`input_tokens + cache_creation_input_tokens + cache_read_input_tokens` of the latest one is the context in use); text blocks (the last 3); `tool_use` blocks: the `command` of Bash calls, the `file_path` of Read, Edit, Write, MultiEdit and NotebookEdit calls (the last 20 files).
- `user` entries: your latest prompt; `tool_result` blocks, for `is_error` and for lx receipts (`[lx: … full output: lx show 12]`) on their own line.
- `system` entries with `subtype: compact_boundary`: `compactMetadata.preTokens`, `postTokens` and `preservedMessages`.
- `last-prompt` (your prompt, when no `user` entry in the tail has it) and `ai-title` (shown by `lx ctx`).

Codex: `turn_context.model`, `token_count` events (`last_token_usage.input_tokens`, `model_context_window`), `user_message`, `patch_apply_end`, assistant `message` items, shell calls (`exec_command`, `shell`, `local_shell_call`, `exec`) with their outputs, and `compacted`.

Everything else is skipped. What lx derives lives in memory for the one run and is never written anywhere.

## Focus

Terms come from the agent's last 3 messages (weights 1.0, 0.8, 0.6) and your last prompt (0.8): identifiers (`ParseMode`, `parse_mode`, `engine.Process`), test names (`TestParseMode`, `test_parse_mode`), file names (`modes.go`), error codes (`TS2345`, `E0308`) and short quoted or backticked strings. Common words, URLs and long prose quotes are left out. The files the agent read or edited count too, matched with their directories. At most 30 terms and 20 files.

When a view has to be cut to its budget, lines that name a focus term or file are kept before others, after error lines. Filters that list things (failing tests, grep hits, files in a diff, `tsc` and `eslint` errors) put the matching ones first. The receipt names what the view kept for it: `focus: TestParseMode, modes.go`.

## Pressure

The context in use is compared with the model's window:

| Context used | Budget |
|---|---|
| under 50% | unchanged |
| 50% | ×0.8 |
| 75% | ×0.6, at least 2,500 tokens |
| 90% | ×0.4, at least 1,500 tokens |

The smaller budget never costs an error line: when it would drop one that the usual budget keeps, lx uses the usual budget (or halfway, when that keeps them all). When pressure changed the view, the receipt says `context 92% full`.

The window, in order: `LX_CONTEXT_WINDOW` (`200000`, `200k`, `1m`); the window Codex records; lx's table (1M for `claude-opus-5`, `claude-sonnet-5`, `claude-fable-5`, `claude-mythos-5`, `claude-opus-4-6` to `4-8` and `claude-sonnet-4-6`; 1M for any id ending in `[1m]` or `-1m`; 200K for other Claude models and anything unknown). If the transcript shows more tokens in use than that (usage, or `preTokens` at a compaction), the window becomes 1M, or the larger number.

## Mode

When neither `-m` nor `LX_MODE` is set, lx looks at the end (400 characters) of the agent's latest message, if it came in this turn or the previous one:

- `verify` for *verify*, *make sure*, *confirm*, *sanity check*, *double-check*, *check that/whether … passes/works/compiles*, *see if … passes*, *run … again*, *re-run the tests/build*, *should now pass/be green*;
- `error` for *debug*, *investigate*, *why … fails/crashes/panics*, *root cause*, *trace*, *reproduce*, *figure out why*, *what's going on*, *diagnose*;
- nothing when both kinds appear, when a cue is negated (*no need to verify*, *don't debug*), or for *debug build*, *debug logs* and the like.

An inferred `verify` applies only to a command with a verdict, one whose filter judges the run (test runners, builds, linters, `make`), or to a run that failed. "Let me verify the changes" before `git diff` leaves the diff at its usual size. An inferred `error` applies to any command, since it only shows more.

The mode then works as if it had been passed with `-m`: a failing `verify` run gets the `error` view. The receipt shows where it came from: `mode verify→error (from your last message)`.

A command that `lx tune` loosened gets neither context pressure nor an inferred mode, so its views only ever show more. Focus still orders them.

## Delta

When the agent runs a command again, lx looks for the newest earlier run of the same command (same arguments, same directory) that:

- printed an lx receipt, so its full output is stored (`lx show N`);
- reached the model whole: its Bash call ran the command itself or as `lx <command>` with no lx flags, and nothing cut or redirected its output (no `| tail -20`, which the hook turns into `--fit`, no `> file`, no `LX_MODE=` or `LX_BUDGET=` in front). A receipt that only shows up in another command's output, such as `cat notes.txt`, doesn't count;
- is in the model's context: after the last compaction boundary, or kept by it, and not spilled to a file by the host,
- was run so that its output went straight to the model: not piped (`| tail`, `| grep`), not redirected to a file, not inside `$(…)` and not in the background, and without `lx -m`, `lx -b`, `LX_MODE=` or `LX_BUDGET=` on the command line;
- if it was itself shown as a delta, the runs it builds on are in context too, back to one shown in full;
- and the current run is confirmed (see Subagents above), since otherwise lx can't tell whose context it is looking at.

The current run gets its usual view when it asks for more than that: `-m error`, `-m debug` (or the same `LX_MODE`), a budget over 8,000 tokens, or a command `lx tune` loosened.

If the output is the same (apart from timings) with the same exit code, the view is one line plus the tool's summary: `[lx: output identical to lx show 12 (2 turns ago, still in your context)]`. Otherwise, for commands whose filter can name its failures (`go test`, `jest`, `vitest`, `mocha`, `npm test`, `pytest`, `cargo`, `tsc` and `eslint`), lx compares them:

- new failures, and failures whose text changed, are printed in full;
- failures that are gone are listed as `fixed:`;
- failures that are the same are listed by name and location under `still failing:`, since the model already has their text. Past 25, only those whose location moved are listed, and the rest are counted. When one message repeats in a file, a partial fix reads `fixed: src/a.ts: error TS2554: … (1 of 3)`;
- the tool's summary lines follow.

lx uses the delta only when it is at least 30% smaller than the usual view, when new or changed failures are at most half of all failures, and when every line of the usual view is in the delta, names a failure the delta prints or lists, or was in the earlier run's view as lx printed it. lx rebuilds that view at the smallest budget it could have had, and ignores numbers on lines that are not errors, except assertion messages and lines with a source location, which must match exactly. A failure is listed as fixed only when its lines are gone from the new output. The receipt, the stored full output and the exit code are the same as for any condensed run. `LX_DELTA=0` turns the delta off. After a compaction, the earlier run no longer counts, and the next run gets the usual view.

## Seeing it

`lx ctx` prints what lx would use right now, from inside the agent:

```text
agent       claude-code, session 66666666-7777-4888-9999-aaaaaaaaaaaa
caller      confirmed
transcript  ~/.claude/projects/-work-shop/66666666-7777-4888-9999-aaaaaaaaaaaa.jsonl (last 6 KiB read, 3 turns)
model       claude-sonnet-4-5
context     40,000 of 200,000 tokens (20.0%), window from the model table
compacted   not in the part read
mode        verify, inferred from the latest assistant message
focus       TestSlug 1.0, cart.go 1.0
files       none
runs        go test: lx show 2, failed, 1 turn ago; go test: lx show 1, failed, 2 turns ago
```

Commands appear as their tool and subcommand, never with their arguments. `lx ctx --json` prints the same as JSON. `lx doctor` has a `context` line when it runs under an agent: whether the transcript was found and read, the model and the window.

## Turning it off

| Variable | Effect |
|---|---|
| `LX_CONTEXT=0` | no transcript is read: lx's output is exactly what it is outside an agent, and `lx discover` replays without context |
| `LX_DELTA=0` | focus, pressure and mode stay; a repeated run gets the usual view |
| `LX_CONTEXT_WINDOW=N` | the window in tokens, when lx's table is wrong for your model |
| `-m MODE`, `LX_MODE` | an explicit mode always wins over the inferred one |
