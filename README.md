# lx

**Your coding agent reads every line a command prints. lx makes it read the lines that matter, and it never hides an error.**

[![ci](https://github.com/iheeb1/lx/actions/workflows/ci.yml/badge.svg)](https://github.com/iheeb1/lx/actions/workflows/ci.yml) ![Go 1.26](https://img.shields.io/badge/go-1.26-00ADD8) ![dependencies: 0](https://img.shields.io/badge/dependencies-0-2a78d6) ![tests: 952 + 23 fuzz](https://img.shields.io/badge/tests-952%20%2B%2023%20fuzz-1baf7a) ![license: MIT](https://img.shields.io/badge/license-MIT-eb6834)

`lx` sits between an AI coding agent (Claude Code, Codex, Gemini CLI, Copilot, Cursor…) and the commands it runs (`git`, `go test`, `pytest`, `jest`, `tsc`, `eslint`, `grep`, `find`, `npm`, `docker`…). It prints a condensed view of their output. The exit code stays the same, every error survives, and anything lx removes can be printed back (`lx show 7`).

| | |
|---|---|
| **91%** | fewer tokens across 116 real command outputs from 11 open-source repos (exact o200k counts) |
| **99%** | of application `file:line` locations kept on failing runs, against **61%** for a head+tail cut to the same size |
| **65** | command filters across git, Go, JS/TS, Python, search/listing, builds, HTTP/JSON and containers, plus a shape-aware engine for every other command |
| **0** | dependencies. It is one static Go binary, with no telemetry and no network code (CI fails if a networking package is linked in) |

---

## Before / after

A failing `go test ./...` in [spf13/cobra](https://github.com/spf13/cobra). The raw output is 306 lines, most of it passing tests' logs. Here is what the agent reads through lx:

```text
$ lx go test ./...
--- FAIL: TestMinimumNArgs_WithLessArgs (0.00s)
    args_test.go:73: Expected "requires at least 2 arg(s), only received 1", got "requires at least 2 arg(s), received 1"
--- FAIL: TestMinimumNArgs_WithLessArgs_WithValid (0.00s)
    args_test.go:73: Expected "requires at least 2 arg(s), only received 1", got "requires at least 2 arg(s), received 1"
--- FAIL: TestMinimumNArgs_WithLessArgs_WithValid_WithInvalidArgs (0.00s)
    args_test.go:73: Expected "requires at least 2 arg(s), only received 1", got "requires at least 2 arg(s), received 1"
--- FAIL: TestFind (0.00s)
    --- FAIL: TestFind/[child_-f_child] (0.00s)
        command_test.go:2876: Wrong args
            Expected: [child -f child]
            Got: [-f child]
[error-like lines printed by passing tests (the rest of their output is hidden):]
Error: at least one of the flags in the group [a b] is required [×4]
[… 11 more such lines, trimmed for this README]
FAIL
FAIL	github.com/spf13/cobra	0.655s
ok  	github.com/spf13/cobra/doc	(cached)
FAIL
[hidden: 237 lines of passing-test output]
[lx: 307→29 lines (−71%) · full output: lx show 1]
```

Every failing test, assertion and `file:line` is still there, in the tool's own format. The last line tells the agent the view is condensed and how to get everything back.

## Results

All numbers below come from `make bench` on real captures. They are reproducible: see [Benchmarks](#benchmarks).

![Tokens saved by kind of command](docs/img/savings-by-category.svg)

![The biggest outputs, before and after](docs/img/biggest-outputs.svg)

### Fewer tokens is only half the job

The obvious way to save tokens is to cut output: `| tail -40`, or head+tail truncation. Agents already do this, and it drops the error you needed. This chart takes every **failing** run in the corpus and compares lx with the same output cut blind, **to the same number of tokens as lx's view**:

![Same token budget, very different outcomes](docs/img/fidelity.svg)

At the same size, lx keeps 96% of the distinct error messages against 64% for head+tail and 47% for `tail -40`. For application `file:line` locations it keeps 99% against 61% and 39%. The error messages lx doesn't show are section headers and passing-test titles that the line classifier matches (for example jest's `Summary of all failing tests` and mocha's `error handling` suite name). `bench/cmd/missing` lists every one of them.

### Head to head with rtk

[rtk](https://github.com/rtk-ai/rtk) (Rust Token Killer) is the popular tool in this space and the inspiration for lx. Both tools ran the same 70 read-only commands, live, in the same repositories. Each tool decided for itself whether to wrap a command (`rtk rewrite` / `lx rewrite`), exactly as its agent hook would. Five runs are excluded because rtk couldn't launch the tool in the benchmark environment (vitest through a missing pnpm, and eslint), which says nothing about its filtering. The full method is in [docs/benchmark.md](docs/benchmark.md).

![Head to head on live commands](docs/img/h2h-tokens.svg)

![lx vs rtk: savings and what survives](docs/img/h2h-fidelity.svg)

rtk is terser on some outputs. It summarizes a passing `go test` as one line, lists `find` results compactly, and caps `git log` at 10 commits. lx saves more tokens overall because it also condenses what rtk passes through: minified grep hits, `git blame`, `rg --files`, mocha, `npm ls`. On failing runs lx keeps 95% of the error messages against rtk's 43%, which matters most there. It is also faster:

![Added latency per command](docs/img/overhead.svg)

The agent hook is quick too: `lx hook claude` takes about 5 ms per call including process start, against about 19 ms measured for rtk's hook.

rtk covers more ground: 100+ commands, 18 agents, a large community. lx takes a different position:

| | rtk 0.50 | lx |
|---|---|---|
| Verdict and counts | parsed from text; known false passes, e.g. pytest `3 passed, 1 error` shown as `3 passed` | the tool's own summary line, copied verbatim; the verdict always agrees with the exit code |
| Exit code | preserved, except `golangci-lint` 1 → 0 | always preserved (a property test checks it) |
| Dropped errors | per filter | caught at runtime by an error guard, plus fidelity tests on 116 real captures |
| Lost output | several filters truncate with no way back | every condensed run is stored; `lx show <id> [--grep RE] [--lines A-B]` |
| Unknown commands | passed through raw | shape-aware engine: JSON, logs, path lists, stack traces, repeated lines |
| Token counting | bytes ÷ 4 (≈19% mean error) | pre-tokenizer estimator (≈6% mean error vs o200k), [details](docs/tokens.md) |
| Savings accounting | head/tail counted as saving the whole file; negatives clamped | signed, measured against what the command actually printed |
| Model-written `rtk git push` | escapes `Bash(git push:*)` deny rules | `lx git push` is checked against deny rules as `git push` |
| Commands run | often twice (e.g. `git status`) | exactly once, with your exact argv |

## Install

```sh
brew install iheeb1/tap/lx          # macOS / Linux with Homebrew
# or
curl -fsSL https://github.com/iheeb1/lx/releases/latest/download/install.sh | sh
lx version
```

The script picks the archive for your system (macOS or Linux, amd64 or arm64) and checks it against the release's `SHA256SUMS`. It then installs one static binary to `~/.local/bin`:

- It never uses sudo, and it tells you when that directory isn't on your PATH.
- If the checksum doesn't match, it installs nothing.
- `LX_INSTALL_DIR=/somewhere` installs elsewhere, and `LX_VERSION=v0.2.0` pins a release.

Manual download and `gh attestation verify` are described in [docs/releasing.md](docs/releasing.md).

With Go 1.26+:

```sh
go install github.com/iheeb1/lx/cmd/lx@latest   # or: git clone https://github.com/iheeb1/lx && cd lx && make install
```

Windows builds are published too (`lx_windows_amd64.zip`, `lx_windows_arm64.zip`), but they're experimental and untested.

## Use it with your agent

### Claude Code

```sh
lx init             # adds a PreToolUse hook to ~/.claude/settings.json (backup kept, other hooks untouched)
lx init --readonly  # same, and read-only commands (git status, ls, grep…) run without a prompt, as they do without lx
lx init --project   # or only for this repository (.claude/settings.json)
lx init --dry-run   # show the change without writing it
lx init --uninstall
lx doctor           # check that it's all working
```

The hook rewrites Bash commands lx understands (`git status` → `lx git status`) and leaves everything else alone. It looks through transparent wrappers (`env`, `timeout`, `nice`, `time`, `command`) and Python project runners (`uv run`, `uvx`, `poetry run`, `pdm run`, `pipenv run`, `hatch run`, `rye run`, `pipx run`). So `uv run pytest -x` becomes `lx uv run pytest -x`, and the command still runs exactly as written. Commands are never rewritten if they:

- pipe into anything other than `head`/`tail`/`cat`, or into `head`/`tail` without `2>&1`. lx prints stderr on stdout, where the cut could hide an error the raw command would show;
- use file redirects, `$(…)`, heredocs, subshells or loops;
- run watchers, servers, debuggers or interactive tools (`--watch`, `pytest --pdb`, `python -i`, `npm run dev`…), or run in the background;
- ask for machine-readable output (`--porcelain`, `--json`, `-z`, `--format=…`, `git status -s`);
- set `LX_RAW=1` or `LX_OFF=1`.

When the output goes through `| head -n N` or `| tail -n N` (also `-N`, `-nN`, `--lines=N`, or a bare `head`/`tail` for 10), the hook tells lx which lines the cut keeps: `go test ./... 2>&1 | tail -n 40` becomes `lx --fit tail:40 go test ./... 2>&1 | tail -n 40`. A head gets `head:40`. With several cuts the smallest count is used, and a plain `40` when they keep different ends. lx then prints at most 40 lines, receipt included:

- **The command printed 40 lines or fewer.** The cut would have shown them all, so lx never prints anything longer: you get its usual view when that fits, and the output as the command printed it otherwise.
- **It printed more.** lx fits its view into 39 lines plus the receipt, so the cut shows all of it: first every error line (when they fit), then the lines the cut asked for, the last ones for a tail and the first ones for a head.

Byte cuts (`-c`), `tail -n +K` and `head -n -K` get no `--fit`.

The hook calls lx by a name your agent's shell can find. `lx init` asks your login shell where `lx` is. If it isn't on that PATH, the hook is installed with `--prefix /path/to/lx`, so a rewrite never fails with `command not found`, and the receipts name that path too.

Permissions work like this:

- If a deny rule matches the original command, or the command inside a wrapper (`git push` in `uv run git push`), lx doesn't rewrite it, and Claude Code's own deny applies.
- If a deny rule matches lx itself (`Bash(lx:*)`), lx stays out of the way, so the original runs instead of being refused.
- If an ask rule matches, you get the prompt.
- If every part matches an allow rule, the rewrite is approved. Write rules for the original command (`Bash(npm test:*)`), not for its lx form.
- With `lx init --readonly`, a rewrite is also approved when every part of it is a read-only command from lx's fixed table and reads only inside the project (or your `permissions.additionalDirectories`).
  - The table: `git status`/`diff`/`log`/`show`/`blame` and the listing forms of `git branch`, `ls`, `tree`, `du`, `find` (no `-exec`, `-delete`, `-fprint`…), `grep` and `rg` (no `--pre`, `-z`).
  - Plus `cd` into an existing subdirectory, and `| head`/`tail`/`cat` with only flags and a line count.
  - Anything else gets the normal prompt: an unknown flag, a path outside the project (symlinks are resolved first), `~`, `$VAR`, an env assignment, a wrapper, a redirection, or under zsh an unquoted `^`, `#` or `{…}`.
  - It never applies in `bypassPermissions` or `dontAsk` mode.
- Otherwise you get the normal prompt.

A command the model writes as `lx …` is checked against your deny and ask rules as if lx weren't there, wrappers included: `lx uv run git push` meets a `Bash(git push:*)` deny rule.

### Other agents

```sh
lx init --agent agents-md   # AGENTS.md block (Codex, and any agent that reads AGENTS.md)
lx init --agent gemini      # Gemini CLI BeforeTool hook
lx init --agent copilot     # GitHub Copilot CLI preToolUse hook
lx init --agent cursor      # Cursor hooks.json
```

For your own tooling, `lx rewrite '<command>'` prints the lx form of a shell command and exits 1 when there is nothing to change (`-v` says why).

## Context-aware

Inside Claude Code or Codex, lx also reads the end of the current session's transcript, so a view fits what the agent is doing right now. Here the agent fixed one of 14 failing tests and runs the same command again:

```text
$ lx go test -v ./...
[lx: same command as lx show 1 (1 turn ago, still in your context) — only what changed:]
fixed: example.com/shop TestTotal
still failing:
  example.com/shop TestSlug/Red_Shoes (cart_test.go:23)
  example.com/shop TestSlug/Blue_Hat (cart_test.go:23)
[… 10 more such lines, trimmed for this README]
FAIL	example.com/shop	0.429s
[62 passed, 13 failed (incl. subtests) · hidden: 137 === RUN/--- PASS lines, 60 lines of passing-test output]
[lx: 250→17 lines (−92%) · mode verify→error (from your last message) · full output: lx show 2]
```

Without the session, lx prints the 12 failures again in full (53 lines), although the model still has them from the first run.

**What it reads.** Only a session transcript on your disk, and only when lx runs under an agent: Claude Code sets `CLAUDE_CODE_SESSION_ID` (`~/.claude/projects/<project>/<session>.jsonl`, or under `CLAUDE_CONFIG_DIR`), Codex sets `CODEX_THREAD_ID` (`~/.codex/sessions/…/rollout-…-<session>.jsonl`, or under `CODEX_HOME`). lx reads at most the last 1 MiB of it, and the last 64 KiB of subagent transcripts changed in the last 5 minutes, to find the one that ran the command. It reads after the command exits, and only when the output is big enough to condense. That adds about 2 ms (with a 30 MB transcript). A missing, unreadable or corrupt transcript means no context, never an error.

**What it derives**, in memory and for this run only:

| | From | What changes |
|---|---|---|
| Focus | identifiers, test names, file names and quoted strings in the agent's last 3 messages and your last prompt, and the files it read or edited | when a view has to be cut, lines and failures that name them are kept first. The receipt says so: `focus: TestParseMode` |
| Context pressure | the tokens the latest turn sent to the model, against its window | from 50% full the budget shrinks (×0.8; ×0.6 from 75%; ×0.4 from 90%, never below 1,500 tokens); error lines keep their room. Receipt: `context 92% full` |
| Mode | cues in the agent's latest message: *verify*, *make sure*, *re-run the tests*, *should pass* → `verify`; *debug*, *investigate*, *why … fails*, *root cause* → `error` | only when neither `-m` nor `LX_MODE` is set. `verify` shrinks only a passing run of a command with a verdict (tests, builds, linters), never a diff or a listing, and a failing run still gets the error view. Receipt: `mode verify (from your last message)` |
| Delta | an earlier run of the same command in the same directory whose receipt is still in the transcript after the last compaction, and whose view reached the model whole (no `\| tail`, `\| head` or lx flags cut it) | only what changed: new and changed failures in full, fixed ones by name, still-failing ones by name and location, and the tool's summary line. Used only when it is at least 30% smaller than the usual view, and never when the run asks for more (`-m error`, `-m debug`, a budget over 8,000) |

The window is 200K tokens, or 1M for models that have it (a table in lx, and ids ending in `[1m]` or `-1m`); a session whose usage has gone past 200K gets 1M too. Codex records its window in the transcript. `LX_CONTEXT_WINDOW=1m` overrides all of this. [docs/context.md](docs/context.md) has the details.

**What it never does.** lx writes nothing from the transcript anywhere: no copy, no cache, no log. It never reads one outside an agent session, and it makes no network calls.

**Seeing it and turning it off.** `lx ctx` prints what lx derives for the current session: agent, model, context used and window, compactions, the mode it infers, focus terms, recent files, and recent runs as tool names (`go test`) with their `lx show` ids. Run it through the agent (or ask the agent to), since only the agent's shell has the session. `--json` is for scripts, and `lx doctor` checks that the transcript is found and readable. `LX_CONTEXT=0` turns everything off and gives back lx's usual output byte for byte. `LX_DELTA=0` turns off only the delta.

## Commands

```text
lx <command> [args…]          run it, print the condensed view, exit with its exit code
lx show [ID|last|last~N] [--errors] [--grep RE] [-C N] [--lines A-B] [--head N] [--tail N] [--full] [--raw]
                              print a stored run back (no id: this project's recent runs; --all: every run)
lx gain [--days N] [--json]   tokens saved so far, by command, with a daily sparkline
lx tune [--json] [--all] [--reset] [CMD]
                              commands whose views lx loosened in this project because the
                              agent read them back in full, and why; --reset forgets them
lx discover [--days N] [--fidelity] [--examples] [--json]
                              replay your real Claude Code transcripts through lx and measure
                              what it would have saved (and which commands it doesn't cover);
                              --fidelity also checks that its views kept what the agent acted on
lx doctor [--json]            check that the hook, PATH, permissions and storage work
lx ctx [--json]               what lx reads from the agent's session: context use, the mode it infers,
                              focus terms, recent runs (see Context-aware)
lx pipe --as "go test ./..."  condense stdin as if it were that command's output
lx rewrite [-v] '<cmd>'       the lx form of a shell command (for hooks and scripts)
lx init / lx hook claude      agent integration
lx filters                    list the built-in filters
lx version                    version, commit, Go version and platform (include it in bug reports)
```

`lx -r <cmd>` or `LX_RAW=1` runs a command untouched, and `lx -b 3000 <cmd>` sets a smaller output budget. `lx --fit tail:40 <cmd>` keeps everything lx prints within 40 lines, for a `| tail -n 40` after it (`head:40` for a head, a plain `40` for either end). The hook adds it.

**Modes.** What an agent needs from an output depends on why it ran the command. `lx -m MODE <cmd>` (or `LX_MODE=MODE`) says why:

| Mode | For | What changes |
|---|---|---|
| `auto` | the default | nothing: a failing run's budget already leans toward the end, where verdicts are |
| `error` | triaging a failure | 1.5× the budget; 3 lines kept after each error line instead of 1; 2 library frames kept on each side of your code in folded stack traces instead of 1 |
| `debug` | active debugging | 2× the budget, `error`'s context and frames, and for commands without a filter, log lines stay lines (no templates) and only runs of 8+ similar lines are folded |
| `verify` | checking that a run is green | half the budget on success, where error lines may fill all of it that the verdict leaves; a failing run gets exactly the `error` view |
| `minimal` | a token diet | at most 2,000 tokens (or your budget, if smaller); error lines may fill all of it that the verdict leaves; a view is shown whenever it saves anything, its receipt included |

Every mode keeps every guarantee below (the exit code, errors first, the error guard, the output limit, `lx show`), and a failing run never gets a smaller budget than in `auto`, except in `minimal`. The receipt names the mode: `[lx: 307→29 lines (−71%) · mode verify→error · full output: lx show 1]`. `-m` overrides `LX_MODE`. An unknown mode in either is an error (exit 2, nothing runs), never a silent `auto`, and the hook then leaves commands alone. A mode scales your budget (`lx -m debug -b 3000` gets 6,000 tokens). An agent can write `LX_MODE=verify go test ./...` too: the hook keeps the assignment. `lx pipe` takes `--mode`. Filters keep what they always keep (`debug` still hides passing tests' logs); they get the mode's budget and, where they fold stack traces through the engine, its frames.

**Getting output back.** `lx show 7` starts with a provenance line, `[lx show 7 · go test ./... · exit 1 · 14 min ago · 307 lines]`. It adds a note when a newer run of the same command exists, or when the run came from another directory.

- `last` is the newest run from the current project, and `last~1` the one before it.
- `--errors` prints the error and warning lines with 3 lines of context.
- `--grep RE -C N` works like `grep -n -C`.
- `--lines A-B`, `--head N` and `--tail N` narrow the selection.

Printed straight to the agent, a long run stops at the last whole line under the agent's output limit and ends with the exact command for the next part. Piped into another program, `lx show` prints everything. `--full` lifts the limit, and `--raw` prints the stored bytes exactly.

**Long runs.** If a command is still running after 30 s, lx says so on stderr (`[lx: still running after 30s · 1,204 lines so far … output so far: lx show 12 --tail 40]`) and stores its output as it arrives. `lx show` then works while the command runs, and still works if lx itself gets killed. If lx is interrupted, it forwards the signal, prints a partial view if the command takes more than 2 s to stop, and never kills the command itself. A command that seems to wait for input (`Ok to proceed? (y)`) gets flagged after 2 s of silence.

**Measuring on your own sessions.** `lx discover` is the honest way to size the benefit before you install the hook. It reads your own session transcripts, finds every Bash call lx would have rewritten, and runs lx's filters on the output that was actually recorded. It also counts outputs that Claude Code had to spill for being over its size limit.

Each command is replayed with the context lx would have had at that point in the transcript: the agent's messages before it (focus and inferred mode) and the context used then (pressure). `LX_CONTEXT=0 lx discover` replays without it.

`--fidelity` checks the other half: did lx keep what your agent needed? When the agent opened or edited a file within 3 tool calls of a command whose output named it at `file:line`, discover checks whether lx's view showed that location, and whether a blind head+tail cut of the same size would have. Only command names (`git status`, `npm test`) and counts appear in the report. `--examples` adds locations from your outputs, and only on your terminal.

**Learning from recalls.** When the agent needs more than a view showed, lx remembers it for that command in that project, and shows more the next time. A *regret* is a condensed run the agent read back whole:

- `lx show N` within 15 minutes of the run, with no `--errors`, `--grep`, `--lines`, `--head` or `--tail`, and not piped into another program;
- `lx show N --full` (or `--raw`) at any time, with no such selection and not piped;
- the same command re-run in the same directory with `lx -r`, `LX_RAW=1 lx` or `LX_OFF=1 lx` within 15 minutes.

Each run counts once. Per project (the nearest directory holding `.git`) and command (tool and subcommand, as in `lx gain`):

- 2 regrets within 7 days **loosen** the command's views: twice the token budget (at most 24,000), and the whole output when it fits in that budget;
- 4 within 7 days make them **raw** until 7 days after the latest one: the whole output. Over the agent's output limit it is still condensed, so the host never swaps it for a preview: that view is the usual one with the most room, so a filter's summary (a templated log, grouped grep hits) stays a summary;
- 20 condensed runs that exit 0 with no regret in between lower the level by one.

A level only ever shows more: under an agent, context pressure, an inferred mode and the delta leave a tuned command alone, and only focus orders its view. The exit code, the error guard, the output limit and `--fit` apply at every level. A tuned view that is still condensed says so in its receipt: `[lx: 2,406→212 lines (−91%) · full output: lx show 14 · loosened after 3 full recalls: lx tune]`, or at the raw level `… · tuned to raw after 4 full recalls, but over the output limit: lx tune]`. `lx tune` lists this project's commands with regrets, their level, why and until when (`--all`: every project; `--json`). `lx tune --reset [CMD]` forgets them. `LX_TUNE=0` turns all of this off, and so does `LX_TRACK=0`.

### Troubleshooting: `lx doctor`

```text
✓ binary      lx v0.2.0 (3f2a1c9, 2026-09-26, go1.26.5, darwin/arm64) at ~/.local/bin/lx
✓ hook        installed in ~/.claude/settings.json: /Users/me/.local/bin/lx hook claude
✓ hook-run    `git status` → `lx git status` in 6 ms
✗ path        lx is not on your shell's PATH (zsh): rewritten commands will fail with command not found
              fix: export PATH=/Users/me/.local/bin:"$PATH"  # or re-run `lx init` so rewrites call lx by its full path
lx doctor: 1 failure
```

It checks:

- **hook:** that the hook is installed, installed only once, and not disabled; that it runs this binary; and it runs it once on `git status`.
- **path:** whether your login shell finds `lx`.
- **rtk:** a conflicting rtk hook.
- **perms:** permission rules that approve *any* command through lx (`Bash(lx:*)`), or that deny lx itself.
- **env:** disabling variables (`LX_HOOK=0`, `LX_RAW=1`, `LX_TEE=0`) and an unknown `LX_MODE`.
- **settings:** invalid settings files.
- **storage:** whether the run store is writable.
- **activity:** Claude Code running while lx records nothing.
- **context:** run from an agent, whether lx finds and can read the session transcript, and the model and window it uses. Outside an agent there is no such line.

It is read-only, and it exits 1 when a check fails.

## What lx does to each tool

| Tool | What the agent sees |
|---|---|
| `git status` | git's own short format (`## main...origin/main [ahead 2]`, `MM file`), conflicts first; mid-rebase or mid-merge hints kept |
| `git log` / `show` / `diff` / `blame` / `branch` | one line per commit, with meaningful body lines kept (BREAKING, Fixes #, Revert…); hunks verbatim; lockfiles and generated files summarized with changed package@versions; conflict markers always flagged |
| `git push` / `pull` / `fetch` / `clone` | progress noise gone; ref updates, rejections and hints verbatim |
| `go test` / `build` / `vet` / `mod` | failing tests with all their output; panics and goroutine dumps grouped by stack; `-json` rendered as text; downloads counted |
| `jest` / `vitest` / `mocha` / `npm test` | every failure block (message, expected/received, code frame, app frames); suites that fail to load always shown; summary lines verbatim; coverage tables reduced to failing rows |
| `tsc` / `eslint` | every error kept with its location; `--pretty` code frames dropped; repeated eslint messages grouped per file with all line:col positions |
| `npm` / `pnpm` / `yarn` / `bun` install, ls, audit, build | deprecations folded into one line (security-related ones kept in full); every `npm error` kept; audit advisories kept, dependency paths counted |
| `pytest` / `pip` / `mypy` / `ruff` / Python tracebacks | failure blocks with `>` and `E` lines and locations; the final `N failed, M passed, K errors` line verbatim; progress dots counted; site-packages frames folded |
| `grep` / `rg` / `git grep` | matches grouped by file, long (minified) lines windowed around the match, per-file caps with exact counts, dependency-directory noise summarized |
| `ls` / `find` / `du` / `tree` | constant columns noted once; path lists as a tree with numbered siblings written as brace expansions (`vite{2..8}.md`) so every path can still be opened |
| `make` / `cc` / `cmake` / `ninja` / `cargo` / `gradle` / `mvn` | compiler commands counted; every diagnostic block kept; include chains folded; `make test` output handed to the inner tool's filter |
| `curl` / `wget` / `httpie` / `jq` / `cat` | large JSON compacted (error fields first, arrays of objects as tables); headers trimmed unless status ≥ 400; source files never altered (large ones windowed with line numbers) |
| `docker` / `kubectl` / `journalctl` | tables trimmed; logs templated (Drain-style) with every error record kept verbatim; stack traces folded, never dropped; BuildKit noise gone |
| `git reflog` / `shortlog` / `worktree list` / `ls-remote` / `submodule status` | kept as git prints them (every line is an item); long lists cut from the end with an exact count |
| `uv run` / `poetry run` / `uvx` / `env` / `timeout` … in front of a command | looked through: `lx poetry run jest` reads exactly like `lx jest`; `lx uv run python` opens its REPL live |
| anything else | generic engine: ANSI and progress stripped, repeated and similar lines collapsed, stack traces folded, JSON/log/path shapes detected |

## Guarantees

These are the invariants lx is built around. Each one is enforced by tests over real captured output:

1. **The exit code is never changed.** lx exits with the child's status, and 128+N if it was killed by a signal.
2. **Error lines are never silently removed.** Command filters keep them, and their fidelity tests prove it on real captures. For everything else, a runtime guard re-appends any error line that went missing. The budget stage keeps error lines before anything else. If error lines alone exceed the output limit, the rest become counted `… N lines omitted …` markers, and `lx show N --errors` prints them all. A [delta](#context-aware) prints every new or changed failure in full and leaves out only what the model already saw: each line of the usual view is either in the delta or was in the earlier run's view, which the transcript shows is still in context. It names the failures it leaves out, and counts them when more than 25 are unchanged.
3. **Never worse.** If condensing saves less than 10% of the tokens, you get the plain output, unless the plain output is over the agent's output limit (see 8). The same holds in lines: before a `| head -n N` or `| tail -n N` (`lx --fit`), an output of N lines or fewer is never replaced by a longer view. In `minimal` mode the bar is lower but still strict: the view and its receipt must cost fewer tokens than the plain output.
4. **Nothing is lost.** When anything is removed, the full output is stored and the receipt line says how to get it back. Storage is mode 0600 and keeps the newest 1,000 runs plus every run from the last 24 hours, capped at 7 days and 256 MiB. Run ids never repeat, and a run is written in one step, so `lx show` never serves a half-written file as complete.
5. **Filters can't hurt you.** A filter that panics or doesn't recognize its input falls back to the generic engine. A bug anywhere else in lx's condensing prints the output unfiltered, with the command's exit code.
6. **The command runs as you wrote it.** It runs once, with your exact argv, stdin and environment. lx never injects flags.
7. **Machine output is untouched.** `--json`, `--porcelain`, `-z`, `--format=…` and similar output is passed through byte for byte. So are watchers, servers and interactive programs, streamed live, including inside wrappers (`uv run python` opens its REPL live).
8. **Every view fits the agent's output limit.** Inside Claude Code, views and `lx show` stay under its Bash output limit (`BASH_MAX_OUTPUT_LENGTH`, 30,000 characters by default). The agent then never gets the host's 2 KB preview in place of the view.
9. **Long runs are never silent.** A command still running after 30 s gets a stderr notice and a live stored run. An interrupted lx forwards the signal, prints a partial view, and never kills the command itself.

## How it works

```mermaid
flowchart LR
    A[agent runs<br/>git status] -->|hook rewrites| B[lx git status]
    B --> C[run once,<br/>capture stdout+stderr]
    C --> D[normalize:<br/>ANSI, \r progress, overstrike]
    D --> E{filter for<br/>this command?}
    E -->|yes| F[command filter]
    E -->|no / bails| G[generic engine:<br/>JSON · logs · paths · stacks · repeats]
    F --> H[error guard]
    G --> H
    H --> I[budget:<br/>errors, then tail, then head]
    I --> J{saves ≥ 10%?}
    J -->|no| K[plain output]
    J -->|yes| L[view + receipt<br/>full copy stored]
```

Under an agent, the session sets the focus, the pressure on the budget and the mode before the filter runs, and a repeated run can then be replaced by its delta.

The budget defaults to 8,000 tokens (`LX_BUDGET`). That keeps every view under Claude Code's ~30k-character limit, beyond which it would truncate or spill the output itself.

Token counts come from an offline estimator that reproduces the cl100k/o200k pre-tokenizer and prices each piece with curves fitted against real `tiktoken` counts. Its mean error is 6%, against 19% for `bytes ÷ 4`. See [docs/tokens.md](docs/tokens.md).

![Counting tokens offline](docs/img/estimator.svg)

## Testing

- **A corpus of real output.** `testdata/corpus` holds 116 captures of real commands (git, go, npm, jest, vitest, mocha, tsc, eslint, pytest, pip, grep, rg, find, ls, curl, make…) run in 11 open-source repositories, including deliberately broken builds and failing tests. Each filter package adds its own real and synthetic variants: 422 more captures.
- **518 golden files.** Each one is the exact text an agent reads for a capture. `make golden` regenerates them, and every diff gets reviewed.
- **Fidelity properties on every capture.** The error guard adds nothing, error messages and `file:line` locations survive, the verdict agrees with the exit code, and a filter bails on localized or unknown formats.
- **23 fuzz targets.** No panics; output is deterministic; the fast classifier equals the reference regex exactly.
- **Adversarial review.** Every filter group was reviewed by a second engineer whose job was to produce false passes, lost errors and slow cases. Each bug found has a regression test.
- **Scale.** Timing tests feed every filter 50,000-line inputs to catch algorithmic blowups (most take under 100 ms), and the suite passes under `-race`.

```sh
go test ./...          # 952 tests, ~3,700 passing with subtests
go test -race ./...
make golden            # after an intentional output change
```

## Benchmarks

```sh
make bench                                  # corpus → bench/out/results.json (+ views of every capture)
pip install tiktoken && python3 bench/tiktoken_counts.py   # exact cl100k/o200k counts
go run ./bench/cmd/h2h -env … -rtk $(which rtk) -lx ./lx    # live head-to-head (see the file header)
make charts                                 # docs/img/*.svg
```

A note on what these numbers mean. They measure the tokens of **command output** an agent reads, not your total bill. System prompts, file reads, your messages and the model's own output are untouched, and prompt caching changes what re-reading costs. `lx discover` measures the share that applies to *your* sessions.

## Configuration

| Variable | Effect |
|---|---|
| `LX_RAW=1` | run commands untouched |
| `LX_BUDGET=N` | output token budget (default 8000) |
| `LX_MODE=MODE` | why commands run: `auto` (default), `error`, `debug`, `verify` or `minimal` (see [Modes](#commands)). `lx -m MODE` overrides it. An unknown value is an error: lx then exits 2 without running anything (even with `-m`), the hook leaves commands alone, and `lx doctor` warns |
| `LX_TEE=0` | don't store full outputs (`lx show` then has nothing to show) |
| `LX_TRACK=0` | don't record savings for `lx gain`, and don't learn from recalls (`lx tune`) |
| `LX_TUNE=0` | don't loosen views after the agent reads runs back in full, and don't learn from it (`lx tune`) |
| `LX_HOOK=0` | make the agent hook a no-op |
| `LX_TEE_DIR`, `LX_DATA_DIR` | where stored outputs and the savings log live |
| `LX_MAX_CHARS=N` | the agent's output limit in characters; views and `lx show` stay under it. In Claude Code the default comes from `BASH_MAX_OUTPUT_LENGTH` (30,000 if unset), and lx uses 90% of it. Elsewhere there's no limit unless you set one. `0` turns it off |
| `LX_HEARTBEAT=30s` | when a command is still running after this long, say so on stderr and start storing its output (`0` turns it off) |
| `LX_PROMPT_IDLE=2s` | after this much silence, flag a prompt nobody is answering, such as `Ok to proceed? (y)` (`0` turns it off) |
| `LX_CONTEXT=0` | don't read the agent's session transcript: no focus, context pressure, inferred mode or delta (see [Context-aware](#context-aware)). `lx discover` then replays without context too |
| `LX_CONTEXT_WINDOW=N` | the model's context window in tokens (`200000`, `200k`, `1m`), in place of lx's table and the transcript |
| `LX_DELTA=0` | never replace a repeated run with what changed since the earlier one |

**Privacy:** `lx gain` records only the tool and subcommand (`git status`), token counts, the duration and the exit code. It never records arguments or output. Stored full outputs live in your user cache directory with mode 0600, expire after 7 days, and are capped at 256 MiB in total. `lx tune` keeps `tune.json` next to the savings log (mode 0600): command keys, the times and run ids of regrets, and the condensed runs of the last 15 minutes. Projects and directories appear there only as SHA-256 hashes salted with a random local key, arguments only inside such a hash, and entries expire after 30 days. Under an agent, lx reads the end of the session transcript for the run at hand and keeps nothing from it (see [Context-aware](#context-aware)). lx makes no network calls, and CI enforces it: `make nonet` fails the build if a networking package (`net`, `net/http`, `crypto/tls`…) is linked into lx for Linux, macOS or Windows.

## Limitations

- Claude Code's built-in Read, Grep and Glob tools don't go through Bash, so the hook can't see them.
- A rewritten command no longer matches Claude Code's built-in auto-approval of read-only commands. Without `--readonly`, you may be prompted for `lx git status` where `git status` ran silently. `lx init --readonly` restores the silent run for a fixed table of read-only commands (see [Permissions](#claude-code)). Never add `Bash(lx:*)` to your allow rules: lx runs arbitrary commands, and `lx doctor` warns if you have.
- A command that waits for input still waits. lx flags a likely prompt after 2 s of silence, but it can't answer it.
- `--fit` handles line counts only. After a byte cut (`| head -c 2000`), `| tail -n +K` or `| head -n -K`, lx's view and receipt can still be cut. Before a cut of under 5 lines (`| tail -3`), lx prints the output as the command did, so the cut applies exactly as it would without lx.
- Shell aliases and functions named like a supported tool are bypassed by `lx <tool>`, as they are with rtk.
- Filters for cargo, gradle, docker and kubectl are verified against synthetic fixtures in the tools' real formats, because those tools weren't available on the capture machine. Everything else is verified on real captures. Windows is untested.
- Parsers recognize English tool output. For localized output they fall back to the generic engine rather than guess.
- Context awareness is a heuristic. The mode comes from English cue words in the agent's latest message, and focus terms from identifiers and quoted strings, so a message that says one thing and means another can pick the wrong mode or focus (`-m`, `LX_MODE` and `LX_CONTEXT=0` override it). It works in Claude Code and Codex only, the agents that keep a session transcript lx can find.
- A delta needs proof that the earlier run is still in the model's context: its receipt in the transcript, and this command recorded there when lx reads it. A command that finishes before the host writes it down, the first run after a compaction, and an earlier output the host spilled to a file or truncated, piped through `head`, `tail` or `grep`, or redirected get the usual view.
- Loosening (`lx tune`) shows more where lx cut for size: a view trimmed to the token budget, or an output small enough to show whole. A filter's summary of an output too large to show whole, such as a templated log or grouped grep hits, stays the same summary when loosened, and at the raw level too when the output is over the agent's output limit; `lx show N` is still how to read all of it. Inside Claude Code the output limit (about 27,000 characters) usually binds before the token budget, so loosening there mostly means "the whole output when it fits the limit".
- `lx tune` learns only from what goes through lx. A raw re-run counts only as `lx -r …`, `LX_RAW=1 lx …` or `LX_OFF=1 lx …`: the hook leaves a command written as `LX_RAW=1 go test` alone, so lx never sees it. `lx show` counts only when its output is neither piped nor redirected: straight to a terminal, or to the agent as Claude Code captures it (stdout and stderr in one file). In a host that captures output through a pipe, `lx show N` looks like `lx show N | grep x`, so there only raw re-runs count. Commands are keyed as in `lx gain`, so `npm run build` and `npm run lint` share `npm run`, and a person reading a run back with `lx show` in a terminal counts the same as the agent.

## Adding a filter

1. Capture real output: run the command and save stdout+stderr, not in a TTY.
2. Add a type with `Name`, `Match` and `Apply` to a package under `internal/filters/` and `engine.Register` it. `Apply` returns `ok=false` for anything it doesn't positively recognize.
3. Add a golden test and the fidelity checks (see `internal/filters/git/status_test.go`).
4. Run `go test ./...` and `make bench`, then look at the view as the agent would.

## License

MIT
