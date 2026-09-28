package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxCapture = 1 << 20

func ExecWith(env []string) func(ctx context.Context, name string, args []string, stdin string) (string, error) {
	return func(ctx context.Context, name string, args []string, stdin string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb capped
		cmd.Stdout, cmd.Stderr = &out, &errb
		cmd.WaitDelay = time.Second
		detach(cmd)
		err := cmd.Run()
		if err != nil {
			if msg := firstLine(errb.String()); msg != "" {
				err = &runError{err: err, stderr: msg}
			}
		}
		return out.String(), err
	}
}

type runError struct {
	err    error
	stderr string
}

func (e *runError) Error() string { return fmt.Sprintf("%v: %s", e.err, e.stderr) }
func (e *runError) Unwrap() error { return e.err }

type capped struct{ buf bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxCapture - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }

func FindProjectDir(cwd, home, configDir string, getenv func(string) string) string {
	if d := getenv("CLAUDE_PROJECT_DIR"); d != "" {
		return d
	}
	if cwd == "" {
		return ""
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	for {
		dot := filepath.Join(dir, ".claude")
		if fi, err := os.Stat(dot); err == nil && fi.IsDir() {
			isUser := (home != "" && filepath.Clean(dir) == filepath.Clean(home)) ||
				(configDir != "" && (filepath.Clean(dot) == filepath.Clean(configDir) || sameFile(dot, configDir)))
			if !isUser {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func ManagedSettingsPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return `C:\Program Files\ClaudeCode\managed-settings.json`
	}
	return "/etc/claude-code/managed-settings.json"
}

var errNoExec = errors.New("running programs is disabled")
