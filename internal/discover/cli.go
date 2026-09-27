package discover

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Usage is the synopsis of `lx discover`.
const Usage = "lx discover [--days N] [--limit N] [--dir DIR] [--json] [--fidelity] [--examples]"

// Main runs `lx discover` with args (after the subcommand word) and returns
// the exit code: 0, 1 when the transcripts can't be read, 2 on bad usage.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage:", Usage)
		fs.PrintDefaults()
	}
	days := fs.Int("days", 14, "look at transcripts modified in the last N days")
	limit := fs.Int("limit", 0, "scan at most N transcript files (newest first)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	dir := fs.String("dir", "", "transcripts directory (default: $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects)")
	fidelity := fs.Bool("fidelity", false, "also measure acted-on fidelity: did lx's view keep the file:line locations the agent went on to open or edit")
	examples := fs.Bool("examples", false, "with --fidelity: list up to 20 locations lx's view missed (prints paths from your outputs)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "lx discover: unexpected argument %q\nusage: %s\n", fs.Arg(0), Usage)
		return 2
	}
	dirs := []string{*dir}
	if *dir == "" {
		dirs = []string{defaultDir()}
	}
	o := Options{Dirs: dirs, Since: time.Now().AddDate(0, 0, -*days), Limit: *limit,
		Fidelity: *fidelity || *examples, Examples: *examples}
	rep, err := Scan(o)
	if err != nil {
		fmt.Fprintln(stderr, "lx discover:", err)
		return 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	rep.Text(stdout)
	return 0
}

// defaultDir is Claude Code's transcripts directory.
func defaultDir() string {
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".claude")
	}
	return filepath.Join(base, "projects")
}
