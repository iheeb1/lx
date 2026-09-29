package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func subagentInput(cwd, cmd string) string {
	return `{"session_id":"8f7c2a1e-4b1d-4c55-9d0e-2f6a7b3c9d10",` +
		`"transcript_path":"/Users/me/.claude/projects/-Users-me-app/8f7c2a1e-4b1d-4c55-9d0e-2f6a7b3c9d10.jsonl",` +
		`"cwd":` + string(jsonString(cwd)) + `,"permission_mode":"acceptEdits","hook_event_name":"PreToolUse",` +
		`"tool_name":"Bash","tool_input":{"command":` + string(jsonString(cmd)) + `,"description":"Show working tree status"},` +
		`"tool_use_id":"toolu_01XyZ","agent_id":"a1b2c3d4e5","agent_type":"general-purpose"}`
}

func TestWorktreeIsolationLeavesCommandsAlone(t *testing.T) {
	const wt = "/Users/me/app/.claude/worktrees/agent-a1b2c3d4"
	const rewritten = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecisionReason":"lx: condensed output (lx show <id> for full)",` +
		`"updatedInput":{"command":"lx git status","description":"Show working tree status"}}}` + "\n"
	cases := []struct {
		name, cwd, cmd string
		rewrite        bool
	}{
		{"isolated subagent", wt, "git status", false},
		{"isolated subagent, not git", wt, "go test ./...", false},
		{"nested directory of the worktree", wt + "/internal/cli", "git status", false},
		{"cd into a worktree", "/Users/me/app", "cd .claude/worktrees/agent-x && git status", false},
		{"cd by absolute path", "/Users/me/app", "cd " + wt + " && git status", false},
		{"pushd into a worktree", "/Users/me/app", "pushd " + wt + " && git status", false},
		{"main checkout", "/Users/me/app", "git status", true},
		{"no dot", "/Users/me/app/claude/worktrees/x", "git status", true},
		{"other directory name", "/Users/me/app/.claude/worktrees-old/x", "git status", true},
		{"cd elsewhere", "/Users/me/app", "cd web && git status", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withRules(t, "")
			got := runHookFor(t, "claude", subagentInput(c.cwd, c.cmd))
			switch {
			case c.rewrite && c.cmd == "git status" && got != rewritten:
				t.Errorf("got %s, want %s", got, rewritten)
			case c.rewrite && got == "":
				t.Error("no rewrite")
			case !c.rewrite && got != "":
				t.Errorf("rewrote in a worktree-isolated session: %s", got)
			}
		})
	}
}

func TestWorktreeIsolationKeepsDenials(t *testing.T) {
	withRules(t, `{"permissions":{"deny":["Bash(git push:*)"]}}`)
	got := runHookFor(t, "claude", subagentInput("/r/.claude/worktrees/w", "lx git push"))
	want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
		"\"permissionDecisionReason\":\"lx: `git push` matches deny rule Bash(git push:*)\"}}\n"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestWorktreeIsolationThroughSymlinksAndProjectDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "app", ".claude", "worktrees", "w1")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "wt")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	withRules(t, "")
	if got := runHookFor(t, "claude", subagentInput(link, "git status")); got != "" {
		t.Errorf("symlinked worktree cwd rewritten: %s", got)
	}
	if got := runHookFor(t, "claude", subagentInput(root, "cd wt && git status")); got != "" {
		t.Errorf("cd through a symlink into a worktree rewritten: %s", got)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", real)
	if got := runHookFor(t, "claude", subagentInput(root, "git status")); got != "" {
		t.Errorf("CLAUDE_PROJECT_DIR in a worktree, rewritten: %s", got)
	}
}

func TestWorktreeGuardIsClaudeOnly(t *testing.T) {
	isolateCodex(t, "")
	if got := runHookFor(t, "codex", codexInput("/r/.claude/worktrees/w", "git status")); got == "" {
		t.Error("the Codex hook skipped a Claude Code worktree path")
	}
}
