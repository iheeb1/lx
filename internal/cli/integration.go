package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/hook"
)

// integrationCommand handles the agent-facing subcommands. ok=false means
// args[0] is not one of them and should be run as a command.
func integrationCommand(args []string) (int, bool) {
	switch args[0] {
	case "hook":
		return cmdHook(args[1:]), true
	case "rewrite":
		return cmdRewrite(args[1:]), true
	case "init":
		return cmdInit(args[1:]), true
	case "doctor":
		return cmdDoctor(args[1:]), true
	case "discover":
		return runDiscover(args[1:]), true
	}
	return 0, false
}

// cmdHook runs `lx hook <agent> [--readonly] [--prefix PATH | --prefix=PATH]`.
func cmdHook(args []string) int {
	agent, o := parseHookArgs(args)
	cwd, _ := os.Getwd()
	// A hook must never block the agent: errors are swallowed, exit is 0.
	if err := hook.HookWith(agent, os.Stdin, os.Stdout, cwd, o); err != nil && os.Getenv("LX_HOOK_DEBUG") == "1" {
		fmt.Fprintln(os.Stderr, "lx hook:", err)
	}
	return 0
}

// parseHookArgs parses the hook's arguments by hand. Unknown flags are
// ignored: a hook written by a newer lx must still never fail.
func parseHookArgs(args []string) (string, hook.HookOptions) {
	agent := "claude"
	var o hook.HookOptions
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		agent, args = args[0], args[1:]
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--readonly":
			o.ReadOnly = true
		case a == "--prefix":
			// A path is absolute: a following flag is not its value.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				o.Prefix = args[i]
			}
		case strings.HasPrefix(a, "--prefix="):
			o.Prefix = a[len("--prefix="):]
		}
	}
	return agent, o
}

func cmdRewrite(args []string) int { return runRewrite(args, os.Stdout, os.Stderr) }

// runRewrite runs `lx rewrite [-v] <command>`: the lx form on stdout (exit 0),
// or exit 1 when nothing changes. -v explains on stderr: one `target:` line
// per command lx would wrap, or `unchanged:` and the reason.
func runRewrite(args []string, stdout, stderr io.Writer) int {
	verbose := false
	if len(args) > 0 && (args[0] == "-v" || args[0] == "--verbose") {
		verbose, args = true, args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: lx rewrite [-v] '<shell command>'")
		return 2
	}
	in := hook.Inspect(strings.Join(args, " "))
	if verbose {
		if in.Changed {
			for _, t := range in.Targets {
				fmt.Fprintln(stderr, "target:", hook.ShellJoin(t))
			}
		} else {
			fmt.Fprintln(stderr, "unchanged:", in.Reason)
		}
	}
	if !in.Changed {
		return 1
	}
	fmt.Fprintln(stdout, in.Rewritten)
	return 0
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	project := fs.Bool("project", false, "install into ./.claude/settings.json instead of your user settings")
	uninstall := fs.Bool("uninstall", false, "remove lx's hook (leaves everything else untouched)")
	dry := fs.Bool("dry-run", false, "print the resulting settings.json without writing it")
	agent := fs.String("agent", "claude", "claude, or one of: "+strings.Join(snippetAgents, ", ")+" (prints a snippet)")
	readOnly := fs.Bool("readonly", false, "approve read-only commands (git status/diff/log, ls, find, grep, rg, tree, du) as Claude Code does without lx")
	noReadOnly := fs.Bool("no-readonly", false, "remove --readonly from an installed hook (it is kept otherwise)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *readOnly && *noReadOnly {
		fmt.Fprintln(os.Stderr, "lx init: --readonly and --no-readonly contradict each other")
		return 2
	}
	lx, err := hook.LxPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx init:", err)
		return 1
	}
	if *agent != "claude" {
		s, err := hook.Snippet(*agent, lx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "lx init:", err)
			return 2
		}
		fmt.Println(s)
		return 0
	}
	err = hook.InitClaude(hook.InitOptions{
		Global: !*project, LxPath: lx, Uninstall: *uninstall, DryRun: *dry, Out: os.Stdout,
		ReadOnly: *readOnly, NoReadOnly: *noReadOnly,
		Probe: func() (string, error) { return hook.ProbeShellLx(os.Getenv("SHELL"), 3*time.Second) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx init:", err)
		return 1
	}
	return 0
}

var snippetAgents = []string{"codex", "agents-md", "gemini", "copilot", "cursor"}
