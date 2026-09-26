package engine

import (
	"strings"
	"testing"
)

func TestMachineReadable(t *testing.T) {
	cases := map[string]bool{
		"git status":                           false,
		"git status --porcelain":               true,
		"git status --porcelain=v2 -b":         true,
		"git log --pretty=format:%h %s":        true,
		"git log --format=%H":                  true,
		"git log --oneline":                    false,
		"git diff --name-only":                 true,
		"git diff --stat":                      false,
		"git blame -p main.go":                 true,
		"git blame --line-porcelain main.go":   true,
		"git blame main.go":                    false,
		"git rev-parse HEAD":                   true,
		"git --namespace x log":                false,
		"go test ./...":                        false,
		"go test -json ./...":                  false,
		"go list -json ./...":                  true,
		"go vet -json ./...":                   true,
		"ls --format=long":                     false,
		"ls --format=across":                   false,
		"ruff check --output-format concise":   false,
		"ruff check --output-format=json":      true,
		"ruff check --output-format sarif":     true,
		"pip list --format=columns":            false,
		"pip list --format=json":               true,
		"eslint -f json .":                     true,
		"eslint --format stylish .":            false,
		"jest --reporter json":                 true,
		"vitest --reporter=json":               true,
		"vitest --reporter=verbose":            false,
		"kubectl get pods -o wide":             false,
		"kubectl get pods -o json":             true,
		"kubectl get pods -ojsonpath={.items}": true,
		"kubectl get pods -o yaml":             true,
		"docker ps --format {{.Names}}":        true,
		"docker ps":                            false,
		"grep -rn foo .":                       false,
		"grep -rlZ foo .":                      true,
		"grep -rZ foo .":                       true,
		"grep -rl foo .":                       true,
		"rg -c foo":                            true,
		"find . -print0":                       true,
		"cargo build --message-format=json":    true,
		"npm ls --json":                        true,
		"gh pr list --json number":             true,
	}
	for cmd, want := range cases {
		c := &Context{Argv: strings.Fields(cmd)}
		if got := MachineReadable(c); got != want {
			t.Errorf("MachineReadable(%q) = %v, want %v", cmd, got, want)
		}
	}
}
