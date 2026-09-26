package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/discover"
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
	case "discover":
		return cmdDiscover(args[1:]), true
	}
	return 0, false
}

func cmdHook(args []string) int {
	agent := "claude"
	if len(args) > 0 {
		agent = args[0]
	}
	cwd, _ := os.Getwd()
	// A hook must never block the agent: errors are swallowed, exit is 0.
	if err := hook.Hook(agent, os.Stdin, os.Stdout, cwd); err != nil && os.Getenv("LX_HOOK_DEBUG") == "1" {
		fmt.Fprintln(os.Stderr, "lx hook:", err)
	}
	return 0
}

func cmdRewrite(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: lx rewrite '<shell command>'")
		return 2
	}
	out, ok := hook.Rewrite(strings.Join(args, " "))
	if !ok {
		return 1
	}
	fmt.Println(out)
	return 0
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	project := fs.Bool("project", false, "install into ./.claude/settings.json instead of your user settings")
	uninstall := fs.Bool("uninstall", false, "remove lx's hook (leaves everything else untouched)")
	dry := fs.Bool("dry-run", false, "print the resulting settings.json without writing it")
	agent := fs.String("agent", "claude", "claude, or one of: "+strings.Join(snippetAgents, ", ")+" (prints a snippet)")
	if err := fs.Parse(args); err != nil {
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
	err = hook.InitClaude(hook.InitOptions{Global: !*project, LxPath: lx, Uninstall: *uninstall, DryRun: *dry, Out: os.Stdout})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx init:", err)
		return 1
	}
	return 0
}

var snippetAgents = []string{"codex", "agents-md", "gemini", "copilot", "cursor"}

func cmdDiscover(args []string) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	days := fs.Int("days", 14, "look at transcripts modified in the last N days")
	limit := fs.Int("limit", 0, "scan at most N transcript files (newest first)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	dir := fs.String("dir", "", "transcripts directory (default: $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dirs := []string{*dir}
	if *dir == "" {
		base := os.Getenv("CLAUDE_CONFIG_DIR")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".claude")
		}
		dirs = []string{filepath.Join(base, "projects")}
	}
	rep, err := discover.Scan(discover.Options{Dirs: dirs, Since: time.Now().AddDate(0, 0, -*days), Limit: *limit})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx discover:", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	rep.Text(os.Stdout)
	return 0
}
