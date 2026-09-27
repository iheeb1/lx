package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/hook"
)

// ---- hook -----------------------------------------------------------------

func (s *state) checkHook() {
	for _, f := range s.files {
		if f.DisableAllHooks {
			s.add("hook", Fail, `"disableAllHooks": true in `+s.show(f.Path)+": Claude Code runs no hooks, lx's included",
				s.editFix(f, `remove "disableAllHooks" from `+s.show(f.Path)))
		}
	}
	verified := s.verified()
	for _, h := range s.hooks {
		if h.kind != lxOther {
			continue
		}
		fix := s.replaceFix(h.entry.File)
		if len(verified) > 0 {
			fix = "remove that command from " + s.show(h.entry.File.Path) + " (the lx hook in " +
				s.show(verified[0].entry.File.Path) + " already runs lx)"
		}
		s.add("hook", Warn, "a hook in "+s.show(h.entry.File.Path)+" runs lx but is not a plain `lx hook claude` command, "+
			"so doctor will not run it: "+h.entry.Command, fix)
	}
	switch n := len(verified); {
	case n == 0 && len(s.hooks) > 0:
		// Only unverifiable lx hooks: reported above.
	case n == 0 && len(s.offBash) > 0:
		h := s.offBash[0]
		s.add("hook", Fail, fmt.Sprintf("the lx hook in %s has matcher %q, which never selects the Bash tool",
			s.show(h.entry.File.Path), h.entry.Matcher),
			fmt.Sprintf(`set its "matcher" to "Bash" in %s`, s.show(h.entry.File.Path)))
	case n == 0:
		msg := "no lx hook in Claude Code's settings (checked " + s.whereChecked() + ")"
		if note := s.ignoredDefaultHook(); note != "" {
			msg += "; " + note
		}
		s.add("hook", Fail, msg, s.installFix())
	case n == 1:
		h := verified[0]
		s.add("hook", OK, "installed in "+s.show(h.entry.File.Path)+": "+h.entry.Command, "")
	default:
		same := true
		for _, h := range verified[1:] {
			same = same && h.entry.Command == verified[0].entry.Command
		}
		msg := fmt.Sprintf("installed %d times (%s) with different commands: Claude Code runs each of them on every Bash call",
			n, s.hookPlaces(verified))
		if same {
			// Claude Code deduplicates identical hook commands.
			msg = fmt.Sprintf("installed %d times (%s): the commands are identical, so Claude Code runs it once, "+
				"but the copies drift apart when one is reinstalled", n, s.hookPlaces(verified))
		}
		s.add("hook", Warn, msg, s.dupFix(verified))
	}
	s.checkManagedOnly(verified)
}

// checkManagedOnly: with "allowManagedHooksOnly" in the managed settings,
// Claude Code ignores user and project hooks.
func (s *state) checkManagedOnly(verified []*foundHook) {
	var managed *settingsFile
	for _, f := range s.files {
		if f.Scope == "managed" && f.AllowManagedHooksOnly {
			managed = f
		}
	}
	if managed == nil || len(verified) == 0 {
		return
	}
	for _, h := range verified {
		if h.entry.File.Scope == "managed" {
			return
		}
	}
	s.add("hook", Fail, `"allowManagedHooksOnly": true in your organization's managed settings (`+managed.Path+
		`): Claude Code runs only managed hooks, so the lx hook in `+s.show(verified[0].entry.File.Path)+" never runs",
		"ask your administrator to add the lx hook to the managed settings")
}

// verified lists the lx hooks doctor can check and run.
func (s *state) verified() []*foundHook {
	var out []*foundHook
	for _, h := range s.hooks {
		if h.kind == lxVerified {
			out = append(out, h)
		}
	}
	return out
}

func (s *state) whereChecked() string {
	var dirs []string
	if s.e.ConfigDir != "" {
		d := s.show(s.e.ConfigDir)
		if s.e.Getenv("CLAUDE_CONFIG_DIR") != "" {
			d += " (CLAUDE_CONFIG_DIR)"
		}
		dirs = append(dirs, d)
	}
	if s.e.ProjectDir != "" {
		dirs = append(dirs, s.show(filepath.Join(s.e.ProjectDir, ".claude")))
	}
	if s.e.ManagedPath != "" {
		dirs = append(dirs, "managed settings")
	}
	if len(dirs) == 0 {
		return "nothing: no settings location is known"
	}
	return joinAnd(dirs)
}

// ignoredDefaultHook notes an lx hook in ~/.claude/settings.json that
// Claude Code does not read because the config dir is elsewhere.
func (s *state) ignoredDefaultHook() string {
	if s.e.Home == "" || s.e.ConfigDir == "" {
		return ""
	}
	def := filepath.Join(s.e.Home, ".claude")
	if filepath.Clean(s.e.ConfigDir) == def || sameFile(s.e.ConfigDir, def) {
		return ""
	}
	f := &settingsFile{Path: filepath.Join(def, "settings.json")}
	f.load()
	for _, h := range f.Hooks {
		if _, kind := classify(h.Command, s.e.Home); kind != notLx {
			return s.show(f.Path) + " has one, but Claude Code reads " + s.show(s.e.ConfigDir) + " instead"
		}
	}
	return ""
}

func (s *state) hookPlaces(hooks []*foundHook) string {
	var out []string
	count := map[string]int{}
	for _, h := range hooks {
		p := s.show(h.entry.File.Path)
		if count[p] == 0 {
			out = append(out, p)
		}
		count[p]++
	}
	for i, p := range out {
		if count[p] > 1 {
			out[i] = fmt.Sprintf("%s ×%d", p, count[p])
		}
	}
	return strings.Join(out, ", ")
}

// userSettings is ConfigDir/settings.json, the file `lx init` edits.
func (s *state) userSettings() *settingsFile {
	for _, f := range s.files {
		if f.Scope == "user" && !f.Local {
			return f
		}
	}
	return nil
}

// initRefuses reports whether `lx init` would refuse to write f (symlinked
// file or directory).
func initRefuses(f *settingsFile) bool {
	return f.Symlink != "" || isSymlink(filepath.Dir(f.Path)) || isSymlink(f.Path)
}

func (s *state) exeCommand() string {
	exe := s.e.Executable
	if exe == "" {
		exe = "lx"
	}
	return shellQuote(exe) + " hook claude"
}

func (s *state) snippet() string {
	b, _ := json.Marshal(s.exeCommand())
	return `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":` + string(b) + `}]}]}}`
}

func (s *state) installFix() string {
	if f := s.userSettings(); f != nil && initRefuses(f) {
		link := f.Linked
		if link == "" {
			link = f.Path
			if isSymlink(filepath.Dir(f.Path)) {
				link = filepath.Dir(f.Path)
			}
		}
		target := realPath(f.Path)
		if f.Linked == f.Path && f.Symlink != "" {
			target = f.Symlink
		}
		return "lx init won't write through the symlink " + s.show(link) + "; merge this into " +
			s.show(target) + ": " + s.snippet()
	}
	return "lx init"
}

// initCmd is the `lx init` form that edits f, or "" when init cannot
// (local, managed or symlinked files).
func (s *state) initCmd(f *settingsFile, flags string) string {
	if f.Local || initRefuses(f) {
		return ""
	}
	switch f.Scope {
	case "user":
		return "lx init" + flags
	case "project":
		c := "lx init --project" + flags
		if s.e.ProjectDir != "" && filepath.Clean(s.e.ProjectDir) != filepath.Clean(s.e.Cwd) {
			c = "cd " + shellQuote(s.e.ProjectDir) + " && " + c
		}
		return c
	}
	return ""
}

// editFix is fix, unless f is the organization's managed settings file,
// which the user cannot change.
func (s *state) editFix(f *settingsFile, fix string) string {
	if f.Scope == "managed" {
		return "this comes from your organization's managed settings (" + f.Path + "); ask their administrator"
	}
	return fix
}

func (s *state) replaceFix(f *settingsFile) string {
	return s.editFix(f, "replace that command in "+s.show(f.Path)+" with: "+s.exeCommand())
}

func (s *state) removeFix(f *settingsFile) string {
	if c := s.initCmd(f, " --uninstall"); c != "" {
		return c
	}
	return s.editFix(f, "remove the lx hook from "+s.show(f.Path))
}

func (s *state) dupFix(hooks []*foundHook) string {
	var files []*settingsFile
	count := map[*settingsFile]int{}
	for _, h := range hooks {
		if count[h.entry.File] == 0 {
			files = append(files, h.entry.File)
		}
		count[h.entry.File]++
	}
	if len(files) == 1 {
		f := files[0]
		flags := ""
		for _, h := range hooks {
			if h.cmd.readOnly {
				flags = " --readonly" // reinstalling must not drop it
			}
		}
		if c := s.initCmd(f, " --uninstall"); c != "" {
			return c + " && " + strings.TrimPrefix(s.initCmd(f, flags), "cd "+shellQuote(s.e.ProjectDir)+" && ")
		}
		return "keep one lx hook entry in " + s.show(f.Path)
	}
	// Project copies first: the user-level hook covers every project.
	var fixes []string
	for _, scope := range []string{"project", "user", "managed"} {
		for _, f := range files {
			if f.Scope == scope {
				fixes = append(fixes, s.removeFix(f))
			}
		}
	}
	return "keep one: " + strings.Join(fixes, "  or  ")
}

// hookFixFor is how to point hook h at this binary.
func (s *state) hookFixFor(h *foundHook) string {
	if c := s.initCmd(h.entry.File, ""); c != "" && s.e.Executable != "" {
		return c
	}
	return s.replaceFix(h.entry.File)
}

// ---- hook-binary ----------------------------------------------------------

func (s *state) checkHookBinary() {
	seen := map[string]bool{}
	for _, h := range s.hooks {
		if h.kind != lxVerified || seen[h.cmd.bin] {
			continue
		}
		seen[h.cmd.bin] = true
		if len(seen) > maxHookRuns {
			s.add("hook-binary", Skip, "more lx hook binaries not checked", "")
			break
		}
		file := s.show(h.entry.File.Path)
		switch {
		case h.viaPATH && h.path == "":
			s.add("hook-binary", Fail, "the hook in "+file+" runs `lx` from PATH, and lx is not on this shell's PATH", s.hookFixFor(h))
		case h.missing != "":
			s.add("hook-binary", Fail, fmt.Sprintf("the hook in %s points at %s, which %s", file, s.show(h.path), h.missing), s.hookFixFor(h))
		case h.relative:
			s.add("hook-binary", Warn, "the hook in "+file+" runs lx by the relative path "+h.cmd.bin+
				", which only works when Claude Code starts in the right directory", s.hookFixFor(h))
		case s.isSelf(h.path):
			s.add("hook-binary", OK, s.show(h.path)+" is this binary", "")
		default:
			s.add("hook-binary", Warn, fmt.Sprintf("the hook runs %s (%s), not this binary %s (%s)",
				s.show(h.path), s.versionOf(h.path), s.show(s.e.Executable), s.version()), s.hookFixFor(h))
		}
	}
	if len(seen) == 0 {
		s.add("hook-binary", Skip, "no lx hook command to check", "")
	}
}

func (s *state) isSelf(p string) bool {
	return p != "" && s.e.Executable != "" && (filepath.Clean(p) == filepath.Clean(s.e.Executable) || sameFile(p, s.e.Executable))
}

// canRun reports whether doctor may execute p (see runBlock).
func (s *state) canRun(p string) bool { return s.runBlock(p) == "" }

// Reasons runBlock gives.
const (
	blockInside  = "is inside this project"
	blockProject = "is named only by this project's settings"
)

// runBlock says why doctor must not execute p, or "" when it may. A cloned
// repository's settings or files must never get code run by `lx doctor`:
//
//   - this very binary may always run;
//   - nothing inside the project may: the project directory, or the git
//     work tree holding it or the working directory (a repository can ship
//     a nested .claude directory, so the project directory alone is not
//     its whole extent);
//   - elsewhere, a program runs only when something other than a
//     project's settings vouches for it: the user's or managed settings,
//     a PATH lookup, or the login shell's `command -v lx`. A path that only
//     a project's settings name is never run, wherever it points.
func (s *state) runBlock(p string) string {
	if p == "" {
		return "is unknown"
	}
	if s.isSelf(p) {
		return ""
	}
	for _, z := range s.zones() {
		if inside(p, z) {
			return blockInside
		}
	}
	if !s.vouched(p) {
		return blockProject
	}
	return ""
}

// zones are the directories whose programs doctor never runs: the project
// and the git work trees holding it or the working directory. A zone that
// is / or holds the home directory is dropped (it would hold everything,
// including the user's own lx).
func (s *state) zones() []string {
	if s.zoneList != nil {
		return *s.zoneList
	}
	var out []string
	for _, d := range []string{s.e.ProjectDir, gitRoot(s.e.Cwd), gitRoot(s.e.ProjectDir)} {
		if d == "" || !filepath.IsAbs(d) || realPath(d) == "/" || (s.e.Home != "" && inside(s.e.Home, d)) {
			continue
		}
		dup := false
		for _, z := range out {
			dup = dup || realPath(z) == realPath(d)
		}
		if !dup {
			out = append(out, d)
		}
	}
	s.zoneList = &out
	return out
}

// gitRoot is the nearest ancestor of dir (dir included) holding a .git
// entry, or "".
func gitRoot(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// vouched reports whether something besides a project's settings names p:
// a hook in the user's or managed settings, a PATH lookup, or the login
// shell.
func (s *state) vouched(p string) bool {
	for _, list := range [][]*foundHook{s.hooks, s.offBash} {
		for _, h := range list {
			if h.path != "" && h.missing == "" && (h.viaPATH || h.entry.File.Scope != "project") && samePath(h.path, p) {
				return true
			}
		}
	}
	if pr := s.probeShell(); pr.path != "" && samePath(pr.path, p) {
		return true
	}
	return false
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || sameFile(a, b)
}

// inside reports whether p is dir or below it (symlinks resolved).
func inside(p, dir string) bool {
	rp, rd := realPath(p), realPath(dir)
	rel, err := filepath.Rel(rd, rp)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel))
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	// Resolve the parent when the leaf does not exist.
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return filepath.Clean(p)
}

// versionOf runs `<p> version` (2 s) and returns its first line.
func (s *state) versionOf(p string) string {
	if s.isSelf(p) {
		return s.version()
	}
	if v, ok := s.versions[p]; ok {
		return v
	}
	v := ""
	if why := s.runBlock(p); why != "" {
		v = "version not checked: it " + why
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
		out, err := s.e.Exec(ctx, p, []string{"version"}, "")
		cancel()
		line, _, _ := strings.Cut(strings.TrimSpace(stripEscapes(out)), "\n")
		line = strings.TrimSpace(line)
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			v = "version unknown: `version` timed out"
		case err != nil || line == "":
			v = "version unknown"
		default:
			v = clip(line, 80)
		}
	}
	s.versions[p] = v
	return v
}

// ---- hook-run -------------------------------------------------------------

func (s *state) checkHookRun() {
	seen := map[string]bool{}
	for _, h := range s.hooks {
		if h.kind != lxVerified || seen[h.entry.Command] {
			continue
		}
		seen[h.entry.Command] = true
		if len(seen) > maxHookRuns {
			break
		}
		s.selfTest(h)
	}
	if len(seen) == 0 {
		msg := "no lx hook to run"
		if len(s.hooks) > 0 {
			msg = "not run: doctor only runs a hook command that is exactly `<path>/lx hook claude [flags]`"
		}
		s.add("hook-run", Skip, msg, "")
	}
}

type hookPayload struct {
	SessionID      string            `json:"session_id"`
	HookEventName  string            `json:"hook_event_name"`
	ToolName       string            `json:"tool_name"`
	ToolInput      map[string]string `json:"tool_input"`
	Cwd            string            `json:"cwd"`
	PermissionMode string            `json:"permission_mode"`
}

func (s *state) payload() string {
	b, _ := json.Marshal(hookPayload{
		SessionID: "lx-doctor", HookEventName: "PreToolUse", ToolName: "Bash",
		ToolInput: map[string]string{"command": probeCmd}, Cwd: s.e.Cwd, PermissionMode: "default",
	})
	return string(b)
}

var errTimeout = errors.New("timed out")

// runHook runs a verified hook as Claude Code would, except that doctor
// execs the argv it verified (the resolved binary and its words) instead of
// handing the settings string to a shell: no shell quirk can make the
// string mean more than what doctor checked.
func (s *state) runHook(h *foundHook, payload string) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	t0 := s.e.Clock()
	out, err := s.e.Exec(ctx, h.path, h.cmd.args, payload)
	dt := s.e.Clock().Sub(t0)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = errTimeout
	}
	return out, dt, err
}

func (s *state) selfTest(h *foundHook) {
	const id = "hook-run"
	switch {
	case runtime.GOOS == "windows":
		s.add(id, Skip, "not run on Windows", "")
		return
	case h.missing != "":
		s.add(id, Skip, "not run: the hook's binary is missing (see hook-binary)", "")
		return
	case h.relPATH:
		s.add(id, Skip, "not run: PATH has relative directories, so the shell might pick a different lx", "")
		return
	case h.relative:
		s.add(id, Skip, "not run: the hook uses a relative path", "")
		return
	case !s.canRun(h.path):
		s.add(id, Skip, "not run: "+s.show(h.path)+" "+s.runBlock(h.path)+", and doctor does not run a program a project chose", "")
		return
	}
	if rule := s.ruleFor(probeCmd); rule != "" {
		s.add(id, Skip, "not run: your deny rule "+rule+" matches `"+probeCmd+"`, so the hook leaves it alone by design", "")
		return
	}
	payload := s.payload()
	byHand := "run it by hand: echo " + shellQuote(payload) + " | " + h.entry.Command

	out, dt, err := s.runHook(h, payload)
	rewritten, bin, problem := s.parseHookOutput(out)
	if err == nil && problem == "" && dt > slowHook {
		// The first run may pay for a cold start; judge the faster of two.
		if out2, dt2, err2 := s.runHook(h, payload); err2 == nil && dt2 < dt {
			if r2, b2, p2 := s.parseHookOutput(out2); p2 == "" {
				rewritten, bin, dt = r2, b2, dt2
			}
		}
	}
	switch {
	case errors.Is(err, errTimeout):
		s.add(id, Fail, fmt.Sprintf("the hook did not answer within %d s; Claude Code waits for it before every Bash call", int(hookTimeout/time.Second)), byHand)
	case err != nil:
		s.add(id, Fail, "the hook failed: "+errText(err), byHand)
	case strings.TrimSpace(out) == "":
		if src := s.hookOffInProcess(); src != "" {
			s.add(id, Fail, "the hook printed nothing for `"+probeCmd+"`: "+src+" turns it off", "unset LX_HOOK")
		} else {
			s.add(id, Fail, "the hook printed nothing for `"+probeCmd+"` (expected a rewrite to `lx "+probeCmd+"`)", byHand)
		}
	case problem != "":
		s.add(id, Fail, problem, byHand)
	default:
		if s.rewriteBin == "" {
			s.rewriteBin = bin
		}
		msg := fmt.Sprintf("`%s` → `%s` in %s", probeCmd, rewritten, fmtLatency(dt))
		if bin != "lx" && !samePath(bin, h.path) {
			// A --prefix naming another program: every rewritten command
			// the agent runs goes through it, not through the hook's lx.
			what := "not the hook's lx " + s.show(h.path)
			if s.runBlock(bin) == blockInside {
				what = "a program inside this project, not the hook's lx " + s.show(h.path)
			}
			s.add(id, Warn, msg+": rewritten commands run "+s.show(bin)+", "+what, s.hookFixFor(h))
			return
		}
		if f, v := s.settingsEnv("LX_HOOK", hookOff); f != nil {
			s.add(id, Warn, msg+" from this shell, but LX_HOOK="+v+" in the env of "+s.show(f.Path)+" turns it off inside Claude Code",
				`remove "LX_HOOK" from "env" in `+s.show(f.Path))
			return
		}
		if dt > slowHook {
			s.add(id, Warn, msg+": slow, and Claude Code waits for it before every Bash call (lx's hook usually takes about 5 ms)",
				"time "+shellQuote(h.path)+" version  # a slow start usually means antivirus scanning or a network home directory")
			return
		}
		s.add(id, OK, msg, "")
	}
}

// parseHookOutput checks the hook's answer: a JSON object whose
// hookSpecificOutput.updatedInput.command is `<lx> git status`.
// bin is the program the rewrite calls: "lx" or an absolute path.
func (s *state) parseHookOutput(out string) (rewritten, bin, problem string) {
	out = strings.TrimSpace(out)
	if out == "" {
		return "", "", "the hook printed nothing"
	}
	var resp struct {
		HookSpecificOutput *struct {
			PermissionDecision       string                     `json:"permissionDecision"`
			PermissionDecisionReason string                     `json:"permissionDecisionReason"`
			UpdatedInput             map[string]json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", "", "the hook printed something that is not JSON: " + clip(firstLine(out), 80)
	}
	hs := resp.HookSpecificOutput
	if hs == nil || hs.UpdatedInput == nil {
		msg := "the hook answered without rewriting `" + probeCmd + "`"
		if hs != nil && hs.PermissionDecisionReason != "" {
			msg += ": " + clip(hs.PermissionDecisionReason, 120)
		}
		return "", "", msg
	}
	var cmd string
	if json.Unmarshal(hs.UpdatedInput["command"], &cmd) != nil {
		return "", "", "the hook's updatedInput has no command"
	}
	w, ok := shellWords(cmd, s.e.Home)
	if !ok || len(w) != 3 || filepath.Base(w[0]) != "lx" || w[1]+" "+w[2] != probeCmd {
		return "", "", "the hook rewrote `" + probeCmd + "` to `" + clip(cmd, 120) + "`, expected `lx " + probeCmd + "`"
	}
	if w[0] != "lx" && !filepath.IsAbs(w[0]) {
		return "", "", "the hook rewrote `" + probeCmd + "` to `" + clip(cmd, 120) + "`, which calls lx by a relative path: it only works in one directory"
	}
	if hs.PermissionDecision == hook.VerdictDeny {
		return "", "", "the hook denied `" + probeCmd + "`: " + clip(hs.PermissionDecisionReason, 120)
	}
	return cmd, w[0], ""
}

// ruleFor returns the user's deny rule matching cmd, if any.
func (s *state) ruleFor(cmd string) string {
	var r hook.Rules
	for _, f := range s.files {
		r.Deny = append(r.Deny, f.Deny...)
	}
	if r.Decide(cmd) != hook.VerdictDeny {
		return ""
	}
	for _, rule := range r.Deny {
		if (hook.Rules{Deny: []string{rule}}).Decide(cmd) == hook.VerdictDeny {
			return rule
		}
	}
	return "?"
}

// hookOff mirrors the hook's own LX_HOOK test.
func hookOff(v string) bool {
	switch strings.ToLower(v) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

func (s *state) hookOffInProcess() string {
	if v := s.e.Getenv("LX_HOOK"); hookOff(v) {
		return "LX_HOOK=" + v + " in your environment"
	}
	return ""
}

// settingsEnv returns the first settings file whose env sets name to a
// value bad reports true for.
func (s *state) settingsEnv(name string, bad func(string) bool) (*settingsFile, string) {
	for _, f := range s.files {
		for _, v := range f.Env {
			if v.Name == name && bad(v.Value) {
				return f, v.Value
			}
		}
	}
	return nil, ""
}
