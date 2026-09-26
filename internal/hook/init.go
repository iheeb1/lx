package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// InitOptions configure InitClaude.
type InitOptions struct {
	Global    bool   // user settings ($CLAUDE_CONFIG_DIR or ~/.claude) instead of ./.claude
	ConfigDir string // the directory holding settings.json; overrides Global (tests)
	LxPath    string // absolute path of the lx binary written into the hook command
	Uninstall bool
	DryRun    bool      // print the resulting settings.json instead of writing it
	Out       io.Writer // messages (nil = discard)
}

// InitClaude installs (or removes) lx's PreToolUse hook in a Claude Code
// settings.json. It edits only its own hook entry: every other key, hook and
// ordering in the file is preserved. It refuses to write through symlinks,
// writes atomically and keeps settings.json.bak with the previous content.
func InitClaude(o InitOptions) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	dir, err := settingsDir(o)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")

	for _, p := range []string{dir, path} {
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			target, _ := filepath.EvalSymlinks(p)
			return fmt.Errorf("refusing to write through symlink %s (→ %s): edit the real file yourself, "+
				"or point CLAUDE_CONFIG_DIR at the real directory", p, target)
		}
		if p == path && !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
	}

	data, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	root := &object{}
	if len(bytes.TrimSpace(data)) > 0 {
		if root, err = parseObject(data); err != nil {
			return fmt.Errorf("%s is not a valid JSON object (%v); not touching it", path, err)
		}
	}

	var changed bool
	var msg string
	if o.Uninstall {
		changed, err = uninstallHook(root)
		msg = "lx: no lx hook found in " + path
	} else {
		lx := o.LxPath
		if lx == "" {
			return errors.New("lx path is required")
		}
		if lx, err = filepath.Abs(lx); err != nil {
			return err
		}
		var warn string
		changed, warn, err = installHook(root, shellQuote(lx)+" hook claude")
		if warn != "" {
			fmt.Fprintln(out, warn)
		}
		msg = "lx: hook already installed in " + path
	}
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	if !changed {
		fmt.Fprintln(out, msg)
		return nil
	}

	result := root.indented()
	if o.DryRun {
		fmt.Fprintf(out, "lx: dry run, would write %s:\n", path)
		_, err := out.Write(result)
		return err
	}

	mode := fs.FileMode(0o600)
	if existed {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	} else {
		dirMode := fs.FileMode(0o755)
		if o.Global {
			dirMode = 0o700
		}
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return err
		}
	}
	if existed {
		if err := writeAtomic(path+".bak", data, mode); err != nil {
			return fmt.Errorf("writing backup: %v", err)
		}
	}
	if err := writeAtomic(path, result, mode); err != nil {
		return err
	}
	if o.Uninstall {
		fmt.Fprintf(out, "lx: removed the lx hook from %s\n", path)
	} else {
		fmt.Fprintf(out, "lx: installed the PreToolUse hook in %s\n", path)
		fmt.Fprintln(out, "    restart Claude Code (or review it under /hooks) for it to take effect")
	}
	if existed {
		fmt.Fprintf(out, "    previous version saved as %s.bak\n", path)
	}
	return nil
}

func settingsDir(o InitOptions) (string, error) {
	switch {
	case o.ConfigDir != "":
		return o.ConfigDir, nil
	case o.Global:
		d := userClaudeDir()
		if d == "" {
			return "", errors.New("cannot locate the Claude config directory; set CLAUDE_CONFIG_DIR")
		}
		return d, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, ".claude"), nil
}

// writeAtomic writes via a temp file in the same directory and a rename, so
// a crash never leaves a half-written file and a symlink at path is replaced
// rather than followed.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".lx-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// isLxHookCommand recognizes `<…/lx> hook claude`, with any path or quoting.
func isLxHookCommand(cmd string) bool {
	l := lex(cmd)
	if l.broken || len(l.toks) != 3 {
		return false
	}
	for _, t := range l.toks {
		if t.kind != tWord {
			return false
		}
	}
	return filepath.Base(l.toks[0].val) == "lx" && l.toks[1].val == "hook" && l.toks[2].val == "claude"
}

// hookEdit walks hooks.PreToolUse[*].hooks[*] and lets fn inspect (and
// replace or drop) each hook object. Only groups and arrays that fn changed
// are re-encoded; everything else keeps its original bytes.
func hookEdit(root *object, fn func(h *object) (keep, changed bool)) (bool, error) {
	hooksRaw, ok := root.get("hooks")
	if !ok {
		return false, nil
	}
	hooks, err := parseObject(hooksRaw)
	if err != nil {
		return false, errors.New(`"hooks" is not an object`)
	}
	preRaw, ok := hooks.get("PreToolUse")
	if !ok {
		return false, nil
	}
	groups, err := parseArray(preRaw)
	if err != nil {
		return false, errors.New(`"hooks.PreToolUse" is not an array`)
	}
	anyChange := false
	var newGroups []json.RawMessage
	for _, gRaw := range groups {
		g, err := parseObject(gRaw)
		if err != nil {
			newGroups = append(newGroups, gRaw)
			continue
		}
		hRaw, ok := g.get("hooks")
		if !ok {
			newGroups = append(newGroups, gRaw)
			continue
		}
		list, err := parseArray(hRaw)
		if err != nil {
			newGroups = append(newGroups, gRaw)
			continue
		}
		groupChanged := false
		var kept []json.RawMessage
		for _, raw := range list {
			h, err := parseObject(raw)
			if err != nil {
				kept = append(kept, raw)
				continue
			}
			keep, changed := fn(h)
			if changed {
				groupChanged = true
			}
			switch {
			case !keep:
			case changed:
				kept = append(kept, h.compact())
			default:
				kept = append(kept, raw)
			}
		}
		if !groupChanged {
			newGroups = append(newGroups, gRaw)
			continue
		}
		anyChange = true
		if len(kept) == 0 {
			continue // the group only held lx's hook
		}
		g.set("hooks", encodeArray(kept))
		newGroups = append(newGroups, g.compact())
	}
	if !anyChange {
		return false, nil
	}
	if len(newGroups) == 0 {
		hooks.del("PreToolUse")
	} else {
		hooks.set("PreToolUse", encodeArray(newGroups))
	}
	if len(hooks.members) == 0 {
		root.del("hooks")
	} else {
		root.set("hooks", hooks.compact())
	}
	return true, nil
}

func installHook(root *object, want string) (changed bool, warn string, err error) {
	found := false
	var others []string
	changed, err = hookEdit(root, func(h *object) (bool, bool) {
		cmd, _ := h.getString("command")
		if !isLxHookCommand(cmd) {
			if reRtk.MatchString(cmd) {
				others = append(others, cmd)
			}
			return true, false
		}
		found = true
		if cmd == want {
			return true, false
		}
		h.set("command", jsonString(want))
		return true, true
	})
	if err != nil {
		return false, "", err
	}
	if len(others) > 0 {
		warn = "lx: warning: another command-rewriting hook is installed (" + strings.Join(others, ", ") +
			"); running both on the same Bash call can conflict"
	}
	if found {
		return changed, warn, nil
	}
	// Append a new matcher group; nothing else is touched.
	hooks := &object{}
	if raw, ok := root.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return false, "", errors.New(`"hooks" is not an object`)
		}
	}
	var groups []json.RawMessage
	if raw, ok := hooks.get("PreToolUse"); ok {
		if groups, err = parseArray(raw); err != nil {
			return false, "", errors.New(`"hooks.PreToolUse" is not an array`)
		}
	}
	entry := &object{}
	entry.set("type", jsonString("command"))
	entry.set("command", jsonString(want))
	group := &object{}
	group.set("matcher", jsonString("Bash"))
	group.set("hooks", encodeArray([]json.RawMessage{entry.compact()}))
	groups = append(groups, group.compact())
	hooks.set("PreToolUse", encodeArray(groups))
	root.set("hooks", hooks.compact())
	return true, warn, nil
}

var reRtk = regexp.MustCompile(`(^|[/\s])rtk(\s|$)`)

func uninstallHook(root *object) (bool, error) {
	return hookEdit(root, func(h *object) (bool, bool) {
		cmd, _ := h.getString("command")
		if isLxHookCommand(cmd) {
			return false, true
		}
		return true, false
	})
}

var reShellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if reShellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// LxPath returns the path to write into hooks: the lx found on $PATH when it
// is this very binary (a stable location like /opt/homebrew/bin/lx that
// survives upgrades), else the absolute path of the running executable.
func LxPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if p, err := exec.LookPath("lx"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			a, errA := os.Stat(abs)
			b, errB := os.Stat(exe)
			if errA == nil && errB == nil && os.SameFile(a, b) {
				return abs, nil
			}
		}
	}
	return filepath.Abs(exe)
}

// Snippet returns copy-paste integration text for agents lx has no
// installer for: "agents-md" and "codex" (instructions for AGENTS.md),
// "copilot", "gemini" and "cursor" (hook configuration JSON, which needs
// `lx hook <agent>`), and "claude" (the settings.json entry InitClaude writes).
func Snippet(agent, lxPath string) (string, error) {
	if lxPath == "" {
		lxPath = "lx"
	}
	q := shellQuote(lxPath)
	cmd := func(sub string) json.RawMessage { return jsonString(q + " hook " + sub) }
	switch agent {
	case "agents-md":
		return "<!-- AGENTS.md: add this section -->\n" + agentsBlock(lxPath), nil
	case "codex":
		return "<!-- Codex: add this section to AGENTS.md in your repo, or to ~/.codex/AGENTS.md for all repos -->\n" +
			agentsBlock(lxPath), nil
	case "claude":
		return "// Claude Code: merge into ~/.claude/settings.json (or run `lx init`)\n" + pretty(obj(
			"hooks", obj("PreToolUse", arr(obj("matcher", jsonString("Bash"), "hooks",
				arr(obj("type", jsonString("command"), "command", cmd("claude")))))))), nil
	case "copilot":
		return "// GitHub Copilot (VS Code agent mode, Copilot CLI): save as .github/hooks/lx.json\n" +
			"// experimental: requires `lx hook copilot`\n" + pretty(obj(
			"version", rawJSON("1"),
			"hooks", obj("PreToolUse", arr(obj("type", jsonString("command"), "command", cmd("copilot"),
				"cwd", jsonString("."), "timeout", rawJSON("5")))))), nil
	case "gemini":
		return "// Gemini CLI: merge into ~/.gemini/settings.json\n" +
			"// experimental: requires `lx hook gemini`; rewritten commands still go through Gemini's confirmation\n" +
			pretty(obj("hooks", obj("BeforeTool", arr(obj("matcher", jsonString("run_shell_command"), "hooks",
				arr(obj("type", jsonString("command"), "command", cmd("gemini")))))))), nil
	case "cursor":
		return "// Cursor: merge into ~/.cursor/hooks.json\n" +
			"// experimental: requires `lx hook cursor`; rewritten commands still go through Cursor's approval\n" +
			pretty(obj("version", rawJSON("1"),
				"hooks", obj("preToolUse", arr(obj("command", cmd("cursor"), "matcher", jsonString("Shell")))))), nil
	}
	return "", fmt.Errorf("unknown agent %q (want one of: agents-md, codex, claude, copilot, gemini, cursor)", agent)
}

// obj builds an ordered JSON object from key, value pairs.
func obj(pairs ...any) json.RawMessage {
	o := &object{}
	for i := 0; i+1 < len(pairs); i += 2 {
		o.set(pairs[i].(string), pairs[i+1].(json.RawMessage))
	}
	return o.compact()
}

func arr(items ...json.RawMessage) json.RawMessage { return encodeArray(items) }

func pretty(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return string(raw) + "\n"
	}
	return b.String() + "\n"
}

func agentsBlock(lxPath string) string {
	where := ""
	if lxPath != "lx" {
		where = fmt.Sprintf("\nlx is installed at `%s` (use that path if `lx` is not on PATH).\n", lxPath)
	}
	return `## Shell commands: prefix with lx

Run build, test, lint, VCS and listing commands through ` + "`lx`" + ` to get condensed
output that still shows every error and keeps the exit code:
` + "`lx git status`, `lx git diff`, `lx go test ./...`, `lx npm test`, `lx pytest -x`, `lx cargo build`, `lx make`" + `.
When lx drops lines it ends with ` + "`[lx: … full output: lx show <id>]`" + `; run that to see everything.

Do not use lx for:
- output you parse or need byte-for-byte: ` + "`--porcelain`, `--json`, `-z`, `--format`, `git rev-parse`, `cat file`" + `
- interactive, watch or follow modes and dev servers: ` + "`-f`, `--watch`, `npm run dev`" + `
- pipelines into anything but head/tail/cat, and commands with file redirects
` + where
}
