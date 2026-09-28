package hook

import (
	"strings"
	"testing"
)

func TestRewriteLeavesBadModeAlone(t *testing.T) {
	t.Setenv("LX_MODE", "")
	for in, want := range map[string]string{
		`LX_MODE=verify go test ./...`:              `LX_MODE=verify lx go test ./...`,
		`LX_MODE= git status`:                       `LX_MODE= lx git status`,
		`env LX_MODE=debug git log`:                 `env LX_MODE=debug lx git log`,
		`LX_MODE="minimal" git status`:              `LX_MODE="minimal" lx git status`,
		`LX_MODE=bogus git status`:                  ``,
		`LX_MODE=Verify git status`:                 ``,
		`env LX_MODE=bogus git status`:              ``,
		`LX_MODE=$M git status`:                     ``,
		`LX_MODE="$M" git status`:                   ``,
		`git status && LX_MODE=bogus go test ./...`: `lx git status && LX_MODE=bogus go test ./...`,
		`uv run env LX_MODE=bogus pytest`:           `lx uv run env LX_MODE=bogus pytest`,
	} {
		if want == "" {
			want = in
		}
		if got, _ := Rewrite(in); got != want {
			t.Errorf("Rewrite(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHookLeavesCommandsAloneOnBadMode(t *testing.T) {
	withRules(t, `{"permissions":{"deny":["Bash(git push --force:*)"]}}`)
	for v, rewrites := range map[string]bool{"": true, "auto": true, "verify": true, "bogus": false, "DEBUG": false, " error": false} {
		t.Setenv("LX_MODE", v)
		out := runHookFor(t, "claude", bashInput("git status"))
		if got := strings.Contains(out, `"lx git status"`); got != rewrites {
			t.Errorf("LX_MODE=%q: rewritten %v: %q", v, got, out)
		}
		if out := runHookFor(t, "claude", bashInput("lx git push --force")); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("LX_MODE=%q: model-written lx git push --force not denied: %q", v, out)
		}
		if rw, _ := EvaluateClaude("git status", t.TempDir(), false, Rules{}); rw != "lx git status" {
			t.Errorf("LX_MODE=%q: EvaluateClaude %q", v, rw)
		}
	}
}
