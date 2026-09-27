package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/hook"
)

// ---- rtk ------------------------------------------------------------------

func (s *state) checkRtk() {
	if len(s.rtk) == 0 {
		s.add("rtk", OK, "no rtk hook", "")
		return
	}
	for _, h := range s.rtk {
		f := h.File
		fix := s.editFix(f, "remove the rtk hook from "+s.show(f.Path))
		switch {
		case f.Scope == "user" && !f.Local:
			fix = "rtk init -g --uninstall"
		case f.Scope == "project" && !f.Local:
			fix = "rtk init --uninstall  # in " + s.show(s.e.ProjectDir)
		}
		s.add("rtk", Warn, "rtk's hook is installed too ("+s.show(f.Path)+": "+clip(h.Command, 80)+
			"): two hooks rewriting the same Bash call can conflict", fix)
	}
}

// ---- perms ----------------------------------------------------------------

func (s *state) checkPerms() {
	found := false
	for _, f := range s.files {
		for _, rule := range f.Allow {
			if broadLxRule(rule) {
				found = true
				s.add("perms", Fail, rule+" in "+s.show(f.Path)+" approves ANY command run through lx",
					s.editFix(f, "remove it and allow specific commands instead, e.g. Bash(lx git status:*)"))
			}
		}
	}
	if s.checkLxGuards() {
		found = true
	}
	if !found {
		s.add("perms", OK, "no allow rule approves every lx command", "")
	}
}

// checkLxGuards reports deny and ask rules aimed at lx itself: they match
// the hook's rewrite of the probe but not the probe, so Claude Code refuses
// (or prompts for) the commands the hook rewrites, while the same command
// typed without lx would run. The bare form is always checked: receipts
// tell the agent to type `lx show <id>`.
func (s *state) checkLxGuards() (found bool) {
	type form struct{ cmd, what, effect string }
	forms := []form{{"lx " + probeCmd, "the hook's rewrite of `" + probeCmd + "`", "the commands lx rewrites"}}
	if b := s.rewriteBin; b != "" && b != "lx" {
		// Rewrites call lx by its full path; the bare form is what the
		// agent types itself (lx show <id>).
		forms = []form{
			{shellQuote(b) + " " + probeCmd, "the hook's rewrite of `" + probeCmd + "`", "the commands lx rewrites"},
			{"lx " + probeCmd, "the form the agent types itself", "the `lx …` commands the agent types (such as lx show <id>)"},
		}
	}
	for _, f := range s.files {
		for _, verdict := range []string{hook.VerdictDeny, hook.VerdictAsk} {
			rules := f.Deny
			if verdict == hook.VerdictAsk {
				rules = f.Ask
			}
			for _, rule := range rules {
				one := hook.Rules{Deny: []string{rule}}
				if verdict == hook.VerdictAsk {
					one = hook.Rules{Ask: []string{rule}}
				}
				if one.Decide(probeCmd) != "" {
					continue // it is about the command, not about lx
				}
				for _, fm := range forms {
					if one.Decide(fm.cmd) != verdict {
						continue
					}
					found = true
					where := rule + " in " + s.show(f.Path) + " matches `" + fm.cmd + "`, " + fm.what
					if verdict == hook.VerdictDeny {
						s.add("perms", Fail, "deny rule "+where+", so Claude Code refuses "+fm.effect,
							s.editFix(f, "remove it: lx applies your deny rules to the command it wraps (a Bash(git push:*) deny also stops `lx git push`)"))
					} else {
						s.add("perms", Warn, "ask rule "+where+", so "+fm.effect+" ask for permission",
							s.editFix(f, "remove it: lx applies your ask rules to the command it wraps"))
					}
					break
				}
			}
		}
	}
	return found
}

// broadLxRule reports whether an allow rule approves lx with any command:
// Bash(lx:*), Bash(lx *), Bash(lx*), the same with a path to lx or with lx
// flags before the wildcard (Bash(lx -r:*)), a wildcard where the command
// goes (Bash(lx * status)), or a command runner followed by anything
// (Bash(lx bash:*), Bash(lx env:*), Bash(lx timeout 5 *)). An exact rule
// such as Bash(lx) only matches `lx` itself and is fine.
func broadLxRule(rule string) bool {
	r := strings.Join(strings.Fields(rule), " ")
	if !strings.HasPrefix(r, "Bash(") || !strings.HasSuffix(r, ")") {
		return false
	}
	p := strings.TrimSpace(r[len("Bash(") : len(r)-1])
	colonStar := strings.HasSuffix(p, ":*")
	p = strings.TrimSpace(strings.TrimSuffix(p, ":*"))
	words := ruleWords(p)
	if len(words) == 0 {
		return false // Bash(*) or Bash(:*) approves everything, lx or not
	}
	w0 := words[0]
	if strings.HasSuffix(w0, "*") {
		// Bash(lx*) is lx followed by anything.
		return filepath.Base(strings.TrimRight(w0, "*")) == "lx"
	}
	if filepath.Base(w0) != "lx" {
		return false
	}
	for i := 1; i < len(words); i++ {
		w := words[i]
		switch {
		case strings.Contains(w, "*"):
			return true
		case w == "-b" || w == "--budget":
			i++ // its value
		case strings.HasPrefix(w, "-"):
			// An lx flag: lx takes every -word before the command as its
			// own (and refuses one it does not know), so none of them
			// narrows what runs.
		case isRunner(filepath.Base(w)):
			return anyAfterRunner(words[i+1:], colonStar)
		default:
			return false // a fixed command word: the rule is specific
		}
	}
	return colonStar
}

// isRunner: these run the command that follows them, so approving
// `lx bash` with anything after it approves every command. (A switch, not
// a map: package init stays free, and lx starts on every agent command.)
func isRunner(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "fish", "env", "sudo", "doas", "xargs", "nice",
		"nohup", "timeout", "time", "command", "exec", "eval", "stdbuf", "lx":
		return true
	}
	return false
}

// anyAfterRunner: the words after a runner leave the command open when
// they are only options, assignments or numbers (timeout 5, nice -n 10,
// env A=1) before the wildcard.
func anyAfterRunner(rest []string, colonStar bool) bool {
	for _, w := range rest {
		switch {
		case strings.Contains(w, "*"):
			return true
		case strings.HasPrefix(w, "-") || strings.Contains(w, "=") || numberish(w):
		default:
			return false
		}
	}
	return colonStar
}

// numberish: 5, 2.5, 10s, 1m (a count or a duration).
func numberish(w string) bool {
	w = strings.TrimRight(w, "smhd")
	if w == "" || w[0] < '0' || w[0] > '9' {
		return false
	}
	for i := 0; i < len(w); i++ {
		if (w[i] < '0' || w[i] > '9') && w[i] != '.' {
			return false
		}
	}
	return true
}

// ruleWords splits a rule pattern on blanks outside quotes and drops the
// quotes, keeping every other character (including *).
func ruleWords(p string) []string {
	var words []string
	var cur strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words
}

// ---- env ------------------------------------------------------------------

type envRule struct {
	name   string
	bad    func(string) bool
	effect string
}

var envRules = []envRule{
	{"LX_HOOK", hookOff, "turns the lx hook into a no-op"},
	{"LX_RAW", is1, "makes lx run every command raw, with nothing condensed"},
	{"LX_OFF", is1, "makes lx run every command raw, with nothing condensed"},
	{"LX_TEE", is0, "stops lx storing full outputs, so a condensed view cannot be recovered with lx show"},
}

func is1(v string) bool { return v == "1" }
func is0(v string) bool { return v == "0" }

func (s *state) checkEnv() {
	found := false
	for _, r := range envRules {
		if v := s.e.Getenv(r.name); r.bad(v) {
			found = true
			s.add("env", Warn, r.name+"="+v+" in your environment "+r.effect, "unset "+r.name+"  # and remove it from your shell's startup files")
		}
		for _, f := range s.files {
			for _, ev := range f.Env {
				if ev.Name == r.name && r.bad(ev.Value) {
					found = true
					s.add("env", Warn, r.name+"="+ev.Value+" in the env of "+s.show(f.Path)+" "+r.effect,
						s.editFix(f, `remove "`+r.name+`" from "env" in `+s.show(f.Path)))
				}
			}
		}
	}
	if !found {
		s.add("env", OK, "LX_HOOK, LX_RAW, LX_OFF and LX_TEE leave lx on", "")
	}
}

// ---- settings -------------------------------------------------------------

func (s *state) checkSettings() {
	const id = "settings"
	var good []string
	problems := false
	linkNoted := map[string]bool{}
	for _, f := range s.files {
		if !f.Exists {
			continue
		}
		p := s.show(f.Path)
		switch {
		case f.ReadErr != nil:
			problems = true
			if f.Dangling {
				s.add(id, Warn, s.show(f.Linked)+" is a symlink to "+s.show(f.Symlink)+", which does not exist, so Claude Code reads no settings from it", "")
				continue
			}
			status := Fail
			if f.Scope == "managed" {
				status = Warn // not the user's file; Claude Code cannot read it either
			}
			s.add(id, status, "cannot read "+p+": "+errText(f.ReadErr), "")
			continue
		case f.ParseErr != nil:
			problems = true
			s.add(id, Fail, p+" is not valid JSON ("+errText(f.ParseErr)+"), so its hooks and permission rules cannot be used",
				"fix the JSON in "+p)
			continue
		case f.Shape != "":
			problems = true
			s.add(id, Warn, p+": "+f.Shape+", so Claude Code cannot read its hooks", "fix the \"hooks\" section of "+p)
		}
		if f.Linked != "" && f.Scope != "managed" && !linkNoted[f.Linked] {
			linkNoted[f.Linked] = true
			s.add(id, OK, s.show(f.Linked)+" is a symlink to "+s.show(f.Symlink)+"; lx init won't write through it (edit the target)", "")
		}
		good = append(good, f.Label)
	}
	switch {
	case len(good) > 0:
		s.add(id, OK, fmt.Sprintf("%s valid: %s", plural(len(good), "settings file"), strings.Join(good, ", ")), "")
	case !problems:
		s.add(id, OK, "no Claude Code settings files yet", "")
	}
}
