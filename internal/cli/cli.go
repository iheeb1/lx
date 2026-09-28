// Package cli implements the lx command line.
package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/track"
)

var Version = "0.1.0-dev"

const usage = `lx — run a command, show your coding agent only what matters.

Usage:
  lx [lx-flags] <command> [args...]   run command, print a condensed view, keep its exit code
  lx show [ID|last|last~N] [--errors] [--grep RE] [-C N] [--lines A-B] [--head N] [--tail N] [--full] [--raw]
                                       print a stored run; straight to the agent it fits the output limit
                                       (no id: this project's recent runs; --all: every run)
  lx gain [--days N] [--top N] [--json]
                                       tokens saved so far
  lx tune [--json] [--all] [--reset] [CMD]
                                       commands whose views lx loosened in this project because the agent
                                       read them back in full; --reset forgets (--all: every project)
  lx discover [--days N] [--json] [--fidelity] [--examples]
                                       measure what lx would save on your real Claude Code sessions
                                       (--fidelity: whether its views kept what the agent acted on)
  lx doctor [--json]                   check that the hook, PATH, permissions and storage are working
  lx ctx [--json]                      what lx reads from the calling agent's session: model, context
                                       use, the mode it infers, focus terms, files and recent runs
  lx init [--project] [--readonly|--no-readonly] [--uninstall] [--dry-run] [--agent NAME]
                                       install the Claude Code hook (or print a snippet for NAME)
  lx rewrite [-v] <command-string>     print the lx form of a shell command (exit 1: unchanged)
  lx hook claude [--readonly] [--prefix PATH]
                                       Claude Code PreToolUse hook (reads JSON on stdin)
  lx pipe [--as "cmd args"] [--exit N] [--mode MODE]
                                       condense stdin as if it were cmd's output
  lx filters                           list built-in filters
  lx version                           version, commit, Go version and platform (include it in bug reports)

lx-flags (before the command):
  -r, --raw          run without condensing (same as LX_RAW=1)
  -b, --budget N     max output tokens (default 8000)
  -m, --mode MODE    why you run the command (default auto, or LX_MODE):
                       error    failure triage: 1.5× budget, 3 lines after each error, more stack frames
                       debug    2× budget, error's context and frames, log lines of unfiltered commands kept
                       verify   green-run check: ½ budget on success; a failure gets the error view
                       minimal  token diet: at most 2,000 tokens, errors still first
                     every mode keeps the exit code, the error guard, errors first and lx show
      --fit [head:|tail:]N
                     print at most N lines, receipt included, for a "| head -n N"
                     (head:N) or "| tail -n N" (tail:N) after lx; the hook adds it
  -v, --verbose      print filter name and token counts to stderr

Environment:
  LX_RAW=1           disable condensing        LX_BUDGET=N    output token budget
  LX_MODE=MODE       the mode of every run (auto, error, debug, verify, minimal; -m overrides it)
  LX_TEE=0           don't store full outputs  LX_TRACK=0     don't record savings (or tune)
  LX_TUNE=0          don't loosen views after full recalls (lx tune)
  LX_HOOK=0          make the hook a no-op     LX_TEE_DIR / LX_DATA_DIR  storage locations
  LX_MAX_CHARS=N     the agent's output limit in characters; views and lx show fit under it
                     (0: no limit; in Claude Code: $BASH_MAX_OUTPUT_LENGTH, else 30000)
  LX_HEARTBEAT=30s   say "still running" and store the output so far after this long (0: off)
  LX_PROMPT_IDLE=2s  flag a prompt nobody answers after this much silence (0: off)
  LX_CONTEXT=0       don't read the agent's session transcript (no focus, context pressure,
                     inferred mode or delta; lx ctx shows what it reads)
  LX_CONTEXT_WINDOW=N  the model's context window in tokens (200k, 1m); default from the model
  LX_DELTA=0         don't replace a repeated run with what changed since the one in context

Everything lx removes is kept: condensed output ends with
  [lx: 1,204→38 lines (−94%) · full output: lx show 7]
`

func Main(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	case "version", "--version", "-V":
		fmt.Println(versionString())
		return 0
	case "show":
		return cmdShow(args[1:])
	case "gain":
		return cmdGain(args[1:])
	case "tune":
		return cmdTune(args[1:])
	case "pipe":
		return cmdPipe(args[1:])
	case "ctx":
		return cmdCtx(args[1:])
	case "filters":
		for _, f := range engine.Filters() {
			fmt.Println(f.Name())
		}
		return 0
	}
	if code, ok := integrationCommand(args); ok {
		return code
	}
	return cmdRun(args)
}

func writeOut(s string) {
	w := bufio.NewWriterSize(os.Stdout, 64*1024)
	w.WriteString(s)
	if s != "" && !strings.HasSuffix(s, "\n") {
		w.WriteByte('\n')
	}
	w.Flush()
}

func cmdKey(argv []string) string {
	c := &engine.Context{Argv: argv}
	name := c.Name()
	sub := c.Sub()
	switch name {
	case "git", "go", "cargo", "npm", "pnpm", "yarn", "bun", "docker", "kubectl", "pip", "pip3",
		"brew", "terraform", "dotnet", "swift", "flutter", "dart", "gh", "uv", "poetry", "npx", "bunx", "make":
		if sub != "" && len(sub) < 24 && !strings.ContainsAny(sub, "/.=") {
			return name + " " + sub
		}
	case "python", "python3":
		if len(argv) > 2 && argv[1] == "-m" {
			return name + " -m " + argv[2]
		}
	}
	return name
}

func cmdGain(args []string) int {
	fs := flag.NewFlagSet("gain", flag.ContinueOnError)
	days := fs.Int("days", 30, "days of history")
	top := fs.Int("top", 15, "commands to list")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	now := time.Now()
	recs, err := track.Load(now.AddDate(0, 0, -*days))
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx gain:", err)
		return 1
	}
	s := track.Summarize(recs, min(*days, 60), now)
	if *asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	s.Text(os.Stdout, *top)
	return 0
}

func cmdPipe(args []string) int {
	fs := flag.NewFlagSet("pipe", flag.ContinueOnError)
	as := fs.String("as", "", "the command that produced stdin, e.g. \"go test ./...\"")
	exit := fs.Int("exit", 0, "exit status of that command")
	budget := fs.Int("budget", 0, "max output tokens")
	stats := fs.Bool("stats", false, "print filter and token stats to stderr")
	var modeName string
	fs.StringVar(&modeName, "mode", "", "why the command ran: "+engine.ModeList+" (default auto, or LX_MODE)")
	fs.StringVar(&modeName, "m", "", "same as --mode")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	mode, err := envMode()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx pipe:", err)
		return 2
	}
	modeSet := false
	fs.Visit(func(f *flag.Flag) { modeSet = modeSet || f.Name == "mode" || f.Name == "m" })
	if modeSet {
		m, ok := engine.ParseMode(modeName)
		if !ok {
			fmt.Fprintf(os.Stderr, "lx pipe: unknown mode %q (the modes are %s)\n", modeName, engine.ModeList)
			return 2
		}
		mode = m
	}
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx pipe:", err)
		return 1
	}
	argv := strings.Fields(*as)
	if len(argv) == 0 {
		argv = []string{"-"}
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	c := &engine.Context{Argv: argv, Exit: *exit, Cwd: cwd, Home: home}
	sess := openSession(argv, cwd, string(in), 0)
	eo := sess.options(c, engine.Options{Budget: *budget, MaxChars: hostCharCap(), Mode: mode}, modeSet || os.Getenv("LX_MODE") != "", false)
	pr := engine.Process(c, string(in), eo)
	out := pr.Output
	if pr.Lossy {
		out += "\n" + sess.receipt(engine.Receipt(pr, ""), pr)
	}
	writeOut(out)
	if *stats {
		fmt.Fprintf(os.Stderr, "lx: filter=%s raw=%d out=%d saved=%.1f%% guard=%d%s\n", pr.Filter, pr.RawTokens, pr.OutTokens, 100*pr.Saved(), pr.GuardAdded, modeStat(pr))
	}
	return 0
}

func filterStreams(c *engine.Context) bool {
	if f, fc := engine.Resolve(c); f != nil {
		if st, ok := f.(engine.Streamer); ok {
			return st.Stream(fc)
		}
	}
	return false
}
