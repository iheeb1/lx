// Package cli is lx's command-line front end.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/runner"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/track"
)

// Version is set at build time with -ldflags "-X github.com/iheeb1/lx/internal/cli.Version=…".
var Version = "0.1.0-dev"

const usage = `lx — run a command, show your coding agent only what matters.

Usage:
  lx [lx-flags] <command> [args...]   run command, print a condensed view, keep its exit code
  lx show [id] [--grep RE] [--lines A-B] [--raw]
                                       print the full output of a condensed run (list if no id)
  lx gain [--days N] [--top N] [--json]
                                       tokens saved so far
  lx discover [--days N] [--json]      measure what lx would save on your real Claude Code sessions
  lx init [--project] [--uninstall] [--dry-run] [--agent NAME]
                                       install the Claude Code hook (or print a snippet for NAME)
  lx rewrite <command-string>          print the lx form of a shell command (exit 1: unchanged)
  lx hook claude                       Claude Code PreToolUse hook (reads JSON on stdin)
  lx pipe [--as "cmd args"] [--exit N] condense stdin as if it were cmd's output
  lx filters                           list built-in filters
  lx version

lx-flags (before the command):
  -r, --raw          run without condensing (same as LX_RAW=1)
  -b, --budget N     max output tokens (default 8000)
  -v, --verbose      print filter name and token counts to stderr

Environment:
  LX_RAW=1           disable condensing        LX_BUDGET=N    output token budget
  LX_TEE=0           don't store full outputs  LX_TRACK=0     don't record savings
  LX_HOOK=0          make the hook a no-op     LX_TEE_DIR / LX_DATA_DIR  storage locations

Everything lx removes is kept: condensed output ends with
  [lx: 1,204→38 lines (−94%) · full output: lx show 7]
`

// Main runs lx and returns the process exit code.
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
		fmt.Println("lx", Version)
		return 0
	case "show":
		return cmdShow(args[1:])
	case "gain":
		return cmdGain(args[1:])
	case "pipe":
		return cmdPipe(args[1:])
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

type runOpts struct {
	raw     bool
	budget  int
	verbose bool
}

func parseRunFlags(args []string) (runOpts, []string, error) {
	o := runOpts{raw: os.Getenv("LX_RAW") == "1" || os.Getenv("LX_OFF") == "1"}
	if b, err := strconv.Atoi(os.Getenv("LX_BUDGET")); err == nil && b > 0 {
		o.budget = b
	}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return o, args[i+1:], nil
		case a == "-r" || a == "--raw":
			o.raw = true
		case a == "-v" || a == "--verbose":
			o.verbose = true
		case a == "-b" || a == "--budget":
			if i+1 >= len(args) {
				return o, nil, errors.New("--budget needs a value")
			}
			i++
			b, err := strconv.Atoi(args[i])
			if err != nil || b <= 0 {
				return o, nil, fmt.Errorf("bad --budget %q", args[i])
			}
			o.budget = b
		case strings.HasPrefix(a, "--budget="):
			b, err := strconv.Atoi(strings.TrimPrefix(a, "--budget="))
			if err != nil || b <= 0 {
				return o, nil, fmt.Errorf("bad %s", a)
			}
			o.budget = b
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return o, nil, fmt.Errorf("unknown lx flag %s (put lx flags before the command)", a)
		default:
			return o, args[i:], nil
		}
	}
	return o, nil, nil
}

func cmdRun(args []string) int {
	o, argv, err := parseRunFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx:", err)
		return 2
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	c := &engine.Context{Argv: argv, Cwd: cwd, Home: home}
	if o.raw || ShouldStream(argv) || engine.MachineReadable(c) || filterStreams(c) {
		return runner.Passthrough(argv)
	}

	res := runner.Run(argv, os.Stdin)
	if res.NotFound {
		fmt.Fprintln(os.Stderr, res.Output)
		return res.ExitCode
	}
	c.Exit = res.ExitCode
	pr := engine.Process(c, res.Output, engine.Options{Budget: o.budget})

	switch {
	case pr.Filter == "passthrough":
		if !res.Replay(os.Stdout, os.Stderr) {
			writeOut(res.Output)
		}
	case !pr.Lossy:
		writeOut(pr.Output)
	default:
		id := 0
		if n, err := tee.Save(tee.Meta{Argv: argv, Cwd: cwd, Exit: res.ExitCode, Filter: pr.Filter}, res.Output); err == nil {
			id = n
		}
		ids := ""
		if id > 0 {
			ids = strconv.Itoa(id)
		}
		writeOut(pr.Output + "\n" + engine.Receipt(pr, ids))
	}
	if o.verbose {
		fmt.Fprintf(os.Stderr, "lx: filter=%s raw=%d out=%d saved=%.1f%% guard=%d %s\n",
			pr.Filter, pr.RawTokens, pr.OutTokens, 100*pr.Saved(), pr.GuardAdded, pr.FilterPanic)
	}
	_ = track.Add(track.Record{
		Cmd: cmdKey(argv), Filter: pr.Filter, Raw: pr.RawTokens, Out: pr.OutTokens,
		Ms: res.Duration.Milliseconds(), Exit: res.ExitCode, Lossy: pr.Lossy,
	})
	return res.ExitCode
}

func writeOut(s string) {
	w := bufio.NewWriterSize(os.Stdout, 64*1024)
	w.WriteString(s)
	if s != "" && !strings.HasSuffix(s, "\n") {
		w.WriteByte('\n')
	}
	w.Flush()
}

// cmdKey is the privacy-preserving label stored in history: the tool and
// its first subcommand word, never arguments.
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

func cmdShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	grep := fs.String("grep", "", "only lines matching this regexp (with line numbers)")
	lines := fs.String("lines", "", "only lines A-B (1-based, inclusive)")
	raw := fs.Bool("raw", false, "print bytes exactly as captured (ANSI included)")
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	if len(pos) == 0 {
		runs := tee.Recent(20)
		if len(runs) == 0 {
			fmt.Println("lx show: no stored outputs yet (they're stored when lx condenses a view)")
			return 0
		}
		for _, m := range runs {
			fmt.Printf("%5d  %s  exit %-3d %-12s %s\n", m.ID, m.Time.Local().Format("01-02 15:04"), m.Exit, m.Filter, strings.Join(m.Argv, " "))
		}
		return 0
	}
	id, err := strconv.Atoi(strings.TrimPrefix(pos[0], "#"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "lx show: bad id %q\n", pos[0])
		return 2
	}
	out, _, err := tee.Load(id)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx show:", err)
		return 1
	}
	if !*raw {
		out = textutil.Clean(out)
	}
	if *grep == "" && *lines == "" {
		writeOut(out)
		return 0
	}
	var re *regexp.Regexp
	if *grep != "" {
		if re, err = regexp.Compile(*grep); err != nil {
			fmt.Fprintln(os.Stderr, "lx show: bad --grep:", err)
			return 2
		}
	}
	lo, hi := 1, 1<<31-1
	if *lines != "" {
		a, b, ok := strings.Cut(*lines, "-")
		lo, _ = strconv.Atoi(a)
		if ok && b != "" {
			hi, _ = strconv.Atoi(b)
		} else if !ok {
			hi = lo
		}
		if lo < 1 || hi < lo {
			fmt.Fprintf(os.Stderr, "lx show: bad --lines %q\n", *lines)
			return 2
		}
	}
	w := bufio.NewWriter(os.Stdout)
	for i, ln := range strings.Split(out, "\n") {
		n := i + 1
		if n < lo || n > hi || re != nil && !re.MatchString(ln) {
			continue
		}
		fmt.Fprintf(w, "%6d  %s\n", n, ln)
	}
	w.Flush()
	return 0
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
	if err := fs.Parse(args); err != nil {
		return 2
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
	pr := engine.Process(c, string(in), engine.Options{Budget: *budget})
	out := pr.Output
	if pr.Lossy {
		out += "\n" + engine.Receipt(pr, "")
	}
	writeOut(out)
	if *stats {
		fmt.Fprintf(os.Stderr, "lx: filter=%s raw=%d out=%d saved=%.1f%% guard=%d\n", pr.Filter, pr.RawTokens, pr.OutTokens, 100*pr.Saved(), pr.GuardAdded)
	}
	return 0
}

// filterStreams asks the matching filter whether this invocation is
// long-running (a watcher, server, or follower) and must not be buffered.
func filterStreams(c *engine.Context) bool {
	if st, ok := engine.Find(c).(engine.Streamer); ok {
		return st.Stream(c)
	}
	return false
}
