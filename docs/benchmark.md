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
`internal/engine/logs_test.go` and the infra filter tests. The
[logs benchmark](#logs-benchcmdlogbench) downloads loghub's samples when it
runs and never stores them in the repository.

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
5. A run is **excluded**, with the reason recorded in `h2h.json`, when a tool
   could not start the command in this environment, as opposed to filtering
   its output. Five rtk runs fall under this: vitest (rtk re-launches it
   through pnpm, which isn't installed here) and eslint (`npm error could not
   determine executable to run`). A run that hangs (a test suite leaving a
   server listening) is killed after 3 minutes and dropped for all three
   variants.

rtk ran in a sandboxed home directory with telemetry disabled. Its SQLite
tracking stayed on, as in a default install, so its timings include that write.
lx's tracking was disabled (`LX_TRACK=0`), which saves it a small append and
the small write of `lx tune`'s state after each condensed run (about 0.5 ms),
and keeps every view untuned.
Neither tool had a warm cache advantage: every variant ran 7 times in turn.

## Logs (`bench/cmd/logbench`)

```sh
go run ./bench/cmd/logbench -lx ./lx -rtk "$(which rtk)" -rtk-env rtk-sandbox.sh \
    -laya-python ~/venvs/laya/bin/python -tiktoken "$(which python3)"
go run ./bench/cmd/charts            # docs/img/logs-h2h.svg
```

`-rescore` scores the views an earlier run saved in `bench/out/logbench/`
again, without running any tool.

**Data.** 14 of [loghub](https://github.com/logpai/loghub)'s 2,000-line
samples: HDFS, Hadoop, Spark, Zookeeper, BGL, HPC, Thunderbird, Linux,
Android, HealthApp, Apache, OpenSSH, OpenStack and Mac, pinned to commit
`dd61d09`. Each `<System>_2k.log` comes with a
`<System>_2k.log_structured.csv` that gives every line its ground-truth event
id and template (`PacketResponder <*> for block blk_<*> terminating`).
logbench downloads both into `bench/.cache/loghub` (gitignored) on its first
run, because their license is research-only. The five log fixtures of lx's
infra filter run too: `docker compose logs`, two `docker logs`, `journalctl`
and `kubectl logs`. They have no templates, so only their tokens and errors
are scored.

**How the agent reads the log.** A stub `docker` on PATH (also installed as
`kubectl` and `journalctl`) prints the log and honours `--tail N`. The agent
runs `docker logs app` for a loghub sample, or the fixture's own command.
As in the head to head, each tool wraps it the way its hook would
(`rtk rewrite`, `lx rewrite`):

| Variant | What runs |
|---|---|
| raw | `docker logs app` |
| rtk | `rtk docker logs app`, rtk's hook rewrite. It asks docker for `--tail 100` |
| rtk log | `rtk log app.log`: rtk's log filter on the whole file. No hook produces it, but it is rtk's best case for logs |
| lx | `lx docker logs app` with `LX_LAYA=0` and no session |
| lx + task | the same inside an agent session that carries a task (below), `LX_LAYA=0` |
| lx + laya | lx with the Laya daemon running |
| lx + laya + task | both |
| lx + laya, 30 s timeout | lx + laya with `LX_LAYA_TIMEOUT=30s`, so that every item lx sends gets judged |

Every lx run has `CLAUDECODE=1`, so Claude Code's output limit applies as it
does in a session. Tracking is off, and each variant gets a fresh store, so
every scored view ends in `lx show 1`. rtk runs in a sandboxed home directory
with telemetry off.

**The task.** lx only learns a task from the agent's session. For the task
variants, logbench writes a Claude Code transcript: a title, the user's
question ("Why are HDFS block transfers failing?") and an assistant message
("Let me read the logs to find out why HDFS block transfers are failing.")
whose pending Bash call is the command. lx sends laya the title and that
sentence as the task. The transcript also gives lx its usual session inputs,
focus terms and an inferred mode: an assistant sentence that reads as
debugging (*why … failing*, *what is causing …*) puts lx in `error` mode, with
1.5× the budget. Five of the 14 loghub tasks do. `lx + task` has the same
session without laya, so the table separates what the session does from what
laya does.

**Laya.** The real daemon (laya 0.3.21, model `convaiinnovations/laya`,
typed-decisions) runs under the sandbox's HOME. logbench starts it with
`lx laya start`. When the lx binary has no `laya` command, as in the published
run, it runs the same embedded `lx_laya.py` with the same offline environment.
lx's default judge timeout of 1.2 s applies except in the 30 s variant.
`logbench.json` records the venv and model sizes, the load time, peak memory,
and for each variant how many requests and items lx sent, how many items the
daemon judged before the deadline in each request, and in how many runs a
template was folded.

**Metrics.**

- **Tokens**: exact o200k counts, with tiktoken, of everything the variant
  printed (stdout and stderr together).
- **Templates shown**: the share of a log's ground-truth templates that the
  view shows. `[×N]` counts and lx's `(routine)` tag are stripped first. A
  view line copied from the log shows the template loghub gives that log
  line, and no other: a looser template that also matches it (`rhost=<*>`
  swallowing `  user=root`) does not count. Some lines are cut short and end
  in `...` or `…`: rtk cuts examples at 100 characters, and lx cuts routine
  lines at 110 and elides the middle of very long ones. A cut line shows a
  template when every log line that starts with its visible part has that
  template, and the visible part holds at least 16 of the template's constant
  characters (or all of them). A cut line that could come from lines of two
  templates shows neither. Lines that are not copied from the log are matched
  against the templates, with each `<*>` read as `.+?`. The match must end at
  the end of the line, because the message is the last field of every loghub
  format. Laya's routine lines carry placeholders of their own (`<*>`, `<N>`…).
  One counts when some log line could match both it and the template, with 16
  constant characters lined up, so a single such line can count for several
  templates. Templates with fewer than 4 constant characters are left out. So
  are the 5 of 1,305 that never match their own raw lines (labelling quirks in
  loghub). As a result, raw output scores 100%.
- **Error kinds**: the same measure over templates that have at least one
  error, warning or fatal line. A line's level comes from the structured CSV's
  `Level` column when that column holds levels (HDFS, Hadoop, Spark,
  Zookeeper, BGL, Android, Apache, OpenStack). Otherwise it comes from
  `engine.Classify`, lx's own classifier, run on the raw line; it is the same
  for every variant. For the fixtures, a kind is a distinct message as in the
  corpus benchmark: the line's words without those that contain digits.
- **Error lines verbatim**: raw error, warning and fatal lines that appear
  whole in the view.
- **Latency**: median wall time of 5 runs, including the shell that starts
  the command. The pooled rows give the median of the 14 logs' medians. The
  first run's output is the one scored. With Laya, the repeats can differ
  from it: how many items the daemon judges before the deadline varies from
  run to run (0 to 16 of 24 on BGL), and so do its folds.

**Blind spots.**

- A template counts as shown on the strength of a single example or count
  line. The measure doesn't say whether the values that matter (which block,
  which host) are in the view. lx summarizes them on `vars:` lines, and rtk
  shows the first example of each group. A `vars:` line never counts as
  showing a template, even when it names one, while a Laya placeholder line
  can count for several.
- Pooled numbers and the chart weight every template equally. Mac, with 337
  templates, weighs 24 times as much as HDFS, with 14.
- The raw row assumes the agent reads everything. In Claude Code, output over
  30,000 characters is cut or saved to a file by the host, so none of the raw
  2,000-line logs would reach the model whole.
- Laya's verdicts depend on how many items it can judge before lx's deadline,
  so they depend on the machine. These runs used an M1 Pro (16 GB), where laya
  runs on the GPU (torch MPS).
- rtk's log filter orders its groups through a hash map, so the examples it
  shows change from run to run.
- loghub's labels have known errors; loghub-2.0 corrects some. A wrong label
  affects every variant alike.
- Where error lines come from lx's classifier (six loghub logs and the
  fixtures), the error measures lean toward lx, whose filter keeps what that
  classifier flags.

## What the numbers are not

- They are **command-output tokens**, not a bill. The model's context also
  holds system prompts, file reads and conversation. Output an agent reads is
  re-sent on later turns, often from the prompt cache at reduced price. How
  much of a session's cost lx removes depends on how much of it is command
  output. `lx discover` measures that on your own transcripts.
- A corpus is a sample. It is weighted toward the ecosystems whose tools were
  installed (Go, Node, Python, C). cargo, gradle, docker and kubectl are
  covered by synthetic fixtures only.
