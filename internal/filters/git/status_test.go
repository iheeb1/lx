package git

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func TestStatusCorpus(t *testing.T) {
	for _, name := range []string{"git-status-dirty", "git-status-dirty-color", "git-status-clean", "git-status-during-merge"} {
		t.Run(name, func(t *testing.T) {
			fc := fixture.Load(t, "git", name)
			c := fc.Context()
			f := engine.Find(c)
			if f == nil || f.Name() != "git-status" {
				t.Fatalf("filter = %v, want git-status", f)
			}
			got, ok := f.Apply(c, fc.Clean())
			if !ok {
				t.Fatal("filter bailed on real output")
			}
			fixture.Golden(t, "git", name, got)
			// Fidelity: every path git listed is still named.
			for _, ln := range strings.Split(fc.Clean(), "\n") {
				if !strings.HasPrefix(ln, "\t") {
					continue
				}
				p := strings.TrimSpace(ln[strings.LastIndex(ln, " ")+1:])
				if !strings.Contains(got, p) {
					t.Errorf("path %q dropped", p)
				}
			}
		})
	}
}

func TestStatusShapes(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ahead", "On branch feat\nYour branch is ahead of 'origin/feat' by 2 commits.\n  (use \"git push\" to publish your local commits)\n\nChanges not staged for commit:\n\tmodified:   a.go\n",
			"## feat...origin/feat [ahead 2]\n M a.go"},
		{"diverged", "On branch x\nYour branch and 'origin/x' have diverged,\nand have 1 and 3 different commits each, respectively.\n  (use \"git pull\" if you want to integrate the remote branch with yours)\n\nnothing to commit, working tree clean",
			"## x...origin/x [ahead 1, behind 3]\nnothing to commit, working tree clean"},
		{"detached", "HEAD detached at 1a2b3c4\nUntracked files:\n  (use \"git add <file>...\" to include in what will be committed)\n\tnew.txt\n\nnothing added to commit but untracked files present (use \"git add\" to track)",
			"## HEAD (detached at 1a2b3c4)\n?? new.txt"},
		{"staged-and-unstaged", "On branch main\nChanges to be committed:\n\tmodified:   a.go\n\trenamed:    b.go -> c.go\n\nChanges not staged for commit:\n\tmodified:   a.go\n\tmodified:   c.go\n",
			"## main\nMM a.go\nRM b.go -> c.go"},
		{"rebase-keeps-hints", "interactive rebase in progress; onto abc123\nLast command done (1 command done):\n   pick def456 msg\nNo commands remaining.\nYou are currently editing a commit while rebasing branch 'main' on 'abc123'.\n  (use \"git commit --amend\" to amend the current commit)\n  (use \"git rebase --continue\" once you are satisfied with your changes)\n\nnothing to commit, working tree clean",
			""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := status{}.Apply(&engine.Context{Argv: []string{"git", "status"}}, tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("expected bail (no branch line), got %q", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("got ok=%v\n%s\nwant\n%s", ok, got, tc.want)
			}
		})
	}
}

func TestStatusBailsOnLocalized(t *testing.T) {
	if _, ok := (status{}).Apply(&engine.Context{Argv: []string{"git", "status"}}, "Sur la branche main\nrien à valider"); ok {
		t.Fatal("must bail on unrecognized (localized) output")
	}
}
