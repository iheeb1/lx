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

func cmdHook(args []string) int {
	agent, o := parseHookArgs(args)
	cwd, _ := os.Getwd()

	if err := hook.HookWith(agent, os.Stdin, os.Stdout, cwd, o); err != nil && os.Getenv("LX_HOOK_DEBUG") == "1" {
		fmt.Fprintln(os.Stderr, "lx hook:", err)
	}
	return 0
}

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
	return runInit(args, os.Stdout, os.Stderr, func() (string, error) {
		return hook.ProbeShellLx(os.Getenv("SHELL"), 3*time.Second)
	})
}

func runInit(args []string, stdout, stderr io.Writer, probe func() (string, error)) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.Bool("project", false, "install into this project (./.claude/settings.json, or ./.codex/hooks.json) instead of your user settings")
	uninstall := fs.Bool("uninstall", false, "remove lx's hook (leaves everything else untouched)")
	dry := fs.Bool("dry-run", false, "print the resulting file without writing it")
	agent := fs.String("agent", "claude", "claude or codex (installs a hook), or one of: "+strings.Join(snippetAgents, ", ")+" (prints a snippet)")
	readOnly := fs.Bool("readonly", false, "approve read-only commands (git status/diff/log, ls, find, grep, rg, tree, du) as Claude Code does without lx")
	noReadOnly := fs.Bool("no-readonly", false, "remove --readonly from an installed hook (it is kept otherwise)")
	portable := fs.Bool("portable", false, "call lx from each machine's PATH, and do nothing where it is missing (for hooks committed to a repository)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usage := func(msg string) int {
		fmt.Fprintln(stderr, "lx init:", msg)
		return 2
	}
	hooked := *agent == "claude" || *agent == "codex"
	switch {
	case *readOnly && *noReadOnly:
		return usage("--readonly and --no-readonly contradict each other")
	case *agent == "codex" && (*readOnly || *noReadOnly):
		return usage("--readonly is for Claude Code; Codex decides approvals by its own policy")
	case *portable && !hooked:
		return usage("--portable works with --agent claude or --agent codex")
	}
	lx, err := hook.LxPath()
	if err != nil {
		fmt.Fprintln(stderr, "lx init:", err)
		return 1
	}
	if !hooked {
		s, err := hook.Snippet(*agent, lx)
		if err != nil {
			return usage(err.Error())
		}
		fmt.Fprintln(stdout, s)
		return 0
	}
	install := hook.InitClaude
	if *agent == "codex" {
		install = hook.InitCodex
	}
	err = install(hook.InitOptions{
		Global: !*project, LxPath: lx, Uninstall: *uninstall, DryRun: *dry, Out: stdout,
		ReadOnly: *readOnly, NoReadOnly: *noReadOnly, Portable: *portable, Probe: probe,
	})
	if err != nil {
		fmt.Fprintln(stderr, "lx init:", err)
		return 1
	}
	return 0
}

var snippetAgents = []string{"agents-md", "gemini", "copilot", "cursor"}
