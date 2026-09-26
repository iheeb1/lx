# Benchmark methodology

Everything in the README's charts can be regenerated from this repository. This
page says exactly what is measured and where the numbers can mislead.

## The corpus

`testdata/corpus/<category>/<name>.txt` holds 116 captures of real commands.
Each has a `.meta.json` with the argv, exit code, repository and a description
of the scenario. The commands ran in public repositories (spf13/cobra,
gin-gonic/gin, vitejs/vite, expressjs/express, moment/luxon, unjs/ufo,
jquense/yup, pallets/click, lua/lua, DaveGamble/cJSON), plus the public GitHub
issues API:

- Captures are **not** from a TTY. stdout and stderr share one pipe, so the
  interleaving is what an agent's Bash tool sees.
- Some scenarios were set up on purpose: failing tests, type errors, merge
  conflicts, a rejected push. Each meta file says what was changed.
- A few captures force color (`*-color`) to exercise ANSI handling.
- Paths were rewritten to `/home/user/src/<repo>` and the capture user to `user`.

The public loghub sample logs used during development are not included. Their
license is research-only. Log handling is tested on synthetic logs generated in
`internal/engine/logs_test.go` and the infra filter tests.

## Corpus benchmark (`make bench`)

For every capture, `bench/cmd/corpusbench` runs lx's full pipeline
(`engine.Process` with every filter registered, default 8,000-token budget).
It records:

- **tokens:** the raw output against lx's view, including the one-line receipt.
  `bench/tiktoken_counts.py` then adds exact `cl100k_base` and `o200k_base`
  counts. The README reports o200k.
- **error messages kept:** among the distinct error-class lines of the raw
  output, how many still have their message in the view. The message is the
  line's words without any token that contains a digit (positions, counts,
  durations), so a regrouped `error Unexpected var … ×9: 7:1 8:1 …` counts as
  keeping all nine lines. Error-class means `engine.Classify`, the same
  classifier lx uses at runtime.
- **app `file:line` locations kept:** distinct `file.ext:line` locations
  outside dependencies and runtimes (`node_modules`, site-packages, GOROOT,
  the Go module cache, `_testmain.go`…). lx folds library frames into counts
  by design, and the README says so.

### Baselines at equal size

For each capture the benchmark also scores what agents do without lx, using
exactly the same metric functions:

- **`| tail -40`**: the last 40 lines.
- **head+tail at lx's size**: the first and last lines of the output, half the
  budget each, cut to the same token count as lx's view. This is the best blind
  truncation can do at that size.

The fidelity chart uses **failing runs only** (exit ≠ 0), because that is where
losing a line costs a fix.

### Known metric blind spots

- `go test -json` output keeps its messages inside JSON strings, so the
  line-based metrics can't match them against the rendered text view.
- The error classifier matches keywords. Test titles such as mocha's
  `error handling`, and headers such as jest's `Summary of all failing tests`,
  count as error lines. lx's views show failing tests' titles but not every
  passing suite title, so these show up as "not kept".
  `go run ./bench/cmd/missing` lists every miss on failing runs.

## Head to head (`bench/cmd/h2h`)

The corpus repositories still contain the deliberate breakage, so their
commands can be re-run live. For every capture whose command is read-only and
repeatable (git status/log/diff/show/branch/blame, go test/build/vet/list,
jest, vitest, mocha, tsc, eslint, npm test/ls, pytest, grep, rg, find, ls, du,
cat, make):

1. The command runs **raw**, **through rtk 0.50.0** and **through lx**, in the
   same directory with the same environment as the original capture.
2. Each tool chooses its own wrapping, the way its agent hook would:
   `rtk rewrite "<cmd>"` (exit 0 or 3 = rewritten) and `lx rewrite "<cmd>"`.
   A command a tool doesn't rewrite runs raw for that tool, which is what
   would happen in a real session.
3. Each variant runs 7 times. The median wall time is reported, and the first
   run's output is scored.
4. Tokens are exact o200k counts. Error messages are scored against the raw
   run with the same function as the corpus benchmark.

rtk ran in a sandboxed home directory with telemetry disabled. Its SQLite
tracking stayed on, as in a default install, so its timings include that write.
lx's tracking was disabled (`LX_TRACK=0`), which saves it a small append.
Neither tool had a warm cache advantage: every variant ran 7 times in turn.

## What the numbers are not

- They are **command-output tokens**, not a bill. The model's context also
  holds system prompts, file reads and conversation. Output an agent reads is
  re-sent on later turns, often from the prompt cache at reduced price. How
  much of a session's cost lx removes depends on how much of it is command
  output. `lx discover` measures that on your own transcripts.
- A corpus is a sample. It is weighted toward the ecosystems whose tools were
  installed (Go, Node, Python, C). cargo, gradle, docker and kubectl are
  covered by synthetic fixtures only.
