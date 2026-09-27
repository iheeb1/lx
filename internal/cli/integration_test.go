package cli

import (
	"bytes"
	"testing"

	"github.com/iheeb1/lx/internal/hook"
)

func TestParseHookArgs(t *testing.T) {
	cases := []struct {
		args  []string
		agent string
		o     hook.HookOptions
	}{
		{nil, "claude", hook.HookOptions{}},
		{[]string{"claude"}, "claude", hook.HookOptions{}},
		{[]string{"gemini"}, "gemini", hook.HookOptions{}},
		{[]string{"claude", "--readonly"}, "claude", hook.HookOptions{ReadOnly: true}},
		{[]string{"--readonly"}, "claude", hook.HookOptions{ReadOnly: true}},
		{[]string{"claude", "--prefix", "/a b/lx", "--readonly"}, "claude", hook.HookOptions{ReadOnly: true, Prefix: "/a b/lx"}},
		{[]string{"claude", "--prefix=/x/lx"}, "claude", hook.HookOptions{Prefix: "/x/lx"}},
		// unknown flags and a dangling --prefix are ignored: a hook never fails
		{[]string{"claude", "--future-flag", "x", "--prefix"}, "claude", hook.HookOptions{}},
		// a flag after --prefix is not its value
		{[]string{"claude", "--prefix", "--readonly"}, "claude", hook.HookOptions{ReadOnly: true}},
	}
	for _, c := range cases {
		agent, o := parseHookArgs(c.args)
		if agent != c.agent || o != c.o {
			t.Errorf("parseHookArgs(%q) = %q %+v, want %q %+v", c.args, agent, o, c.agent, c.o)
		}
	}
}

func TestRewriteVerbose(t *testing.T) {
	cases := []struct {
		args           []string
		code           int
		stdout, stderr string
	}{
		{[]string{"git status"}, 0, "lx git status\n", ""},
		{[]string{"git", "status"}, 0, "lx git status\n", ""},
		{[]string{"echo hi"}, 1, "", ""},
		{[]string{"-v", "cd x && FOO=1 go test ./... && git log --grep='a b'"}, 0,
			"cd x && FOO=1 lx go test ./... && lx git log --grep='a b'\n",
			"target: go test ./...\ntarget: git log '--grep=a b'\n"},
		{[]string{"-v", "git status > out.txt"}, 1, "", "unchanged: no supported command\n"},
		{[]string{"--verbose", "echo $(date)"}, 1, "", "unchanged: command substitution\n"},
		{[]string{"-v"}, 2, "", "usage: lx rewrite [-v] '<shell command>'\n"},
		{nil, 2, "", "usage: lx rewrite [-v] '<shell command>'\n"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := runRewrite(c.args, &out, &errb)
		if code != c.code || out.String() != c.stdout || errb.String() != c.stderr {
			t.Errorf("rewrite %q: code %d stdout %q stderr %q\nwant %d %q %q",
				c.args, code, out.String(), errb.String(), c.code, c.stdout, c.stderr)
		}
	}
}
