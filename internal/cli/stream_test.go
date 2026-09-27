package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/hook"
)

func TestShouldStreamPeelsWrappers(t *testing.T) {
	cases := map[string]bool{
		"uv run python":                                  true,
		"uv run python3.12":                              true,
		"uv run --with rich ipython":                     true,
		"poetry run jest --watch":                        true,
		"poetry run python":                              true,
		"pdm run python":                                 true,
		"uvx ruff check --watch":                         true,
		"env X=1 node":                                   true,
		"FOO=1 uv run python":                            true,
		"env -u X uv run python":                         true,
		"timeout 30 tail -f app.log":                     true,
		"nice -n 5 npm run dev":                          true,
		"time -p pnpm dev":                               true,
		"nohup vite":                                     true,
		"command vim x":                                  true,
		"env FOO=1 docker compose up":                    true,
		"nohup nohup nohup nohup node":                   true, // engine.MaxPeel layers
		"python3.12":                                     true,
		"python3.13t":                                    true,
		"uv run pytest -x":                               false,
		"uv run python script.py":                        false,
		"uv run python -m pytest":                        false,
		"poetry run mypy src":                            false,
		"env X=1 node app.js":                            false,
		"timeout 30 tail -n 5 app.log":                   false,
		"nice -n 5 npm run build":                        false,
		"uv sync":                                        false,
		"env -i node":                                    false, // env -i is not peeled: its semantics differ
		"pythonw x.py":                                   false,
		"python-config":                                  false,
		"nohup nohup nohup nohup nohup node":             false, // past engine.MaxPeel
		"timeout 5 go test ./...":                        false,
		"env GOFLAGS=-count=1 go test ./...":             false,
		"uv run --directory sub pytest --color=yes -x":   false,
		"poetry run pytest -p no:cacheprovider -q tests": false,

		// -i before the script opens the prompt
		"python -i":                            true,
		"python3 -u -i":                        true,
		"uv run python -i script.py":           true,
		"node -i":                              true,
		"bash -i":                              true,
		"python script.py -i":                  false,
		"python -m pip install -i https://x y": false,
		"python -c pass -i":                    false,
		"node app.js -i":                       false,
		"gtimeout 30 tail -f app.log":          true,

		// the command inside is judged by its own name, not the wrapper's
		"env -u X ls -F":                 false,
		"nohup ls -F":                    false,
		"uv run git log --follow f.go":   false,
		"env A=1 grep -F a.b f":          false,
		"timeout 5 docker build -f D .":  false,
		"env -u X tail -F app.log":       true,
		"uv run --with x pytest --watch": true,
		"nice -n 5 kubectl logs -f web":  true,
		"poetry run python -i":           true,
	}
	for cmd, want := range cases {
		if got := ShouldStream(strings.Fields(cmd)); got != want {
			t.Errorf("ShouldStream(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// -f, -F and --follow mean "follow" only for some tools and positions.
func TestShouldStreamFollowFlags(t *testing.T) {
	cases := map[string]bool{
		"git log --follow f.go":                 false, // follows renames
		"docker build -f Dockerfile .":          false, // a file
		"docker compose -f dc.yml logs web":     false,
		"docker compose -f dc.yml ps":           false,
		"docker compose -f dc.yml logs -f web":  true,
		"docker logs -f web":                    true,
		"docker-compose logs -f web":            true,
		"docker-compose up":                     true,
		"docker-compose up -d":                  false,
		"docker compose -f dc.yml run web sh":   true,
		"docker compose -f dc.yml exec web sh":  true,
		"docker compose watch":                  true,
		"docker-compose run web pytest":         true,
		"docker compose -f dc.yml build":        false,
		"docker events":                         true,
		"docker events -f type=container":       true,
		"docker events --until 1m":              false,
		"docker stats":                          true,
		"docker stats --no-stream":              false,
		"docker system prune -f":                false,
		"kubectl apply -f x.yaml":               false,
		"kubectl -n prod logs -f web":           true,
		"grep -F a.b f":                         false,
		"rg -F a.b":                             false,
		"ls -F":                                 false,
		"git grep -F x":                         false,
		"tail -F app.log":                       true,
		"journalctl -F _SYSTEMD_UNIT":           true, // kept as before: not a follower, but not a log either
		"journalctl -f":                         true,
		"tail --follow=name app.log":            true,
		"stern -f api":                          true,
		"timeout 60 docker compose logs -f web": true,
		"env A=1 docker build -f Dockerfile .":  false,

		// short-option clusters: f follows before any option taking a value
		"tail -fn 50 app.log":                     true,
		"tail -n 50 -f app.log":                   true,
		"tail -5f app.log":                        true,
		"tail -Fn 20 app.log":                     true,
		"tail -n 50 app.log":                      false,
		"tail -c 100 app.log":                     false,
		"journalctl -fu nginx":                    true,
		"journalctl -xef":                         true,
		"journalctl -xe":                          false,
		"journalctl -uf":                          false, // unit "f"
		"journalctl -n100 -u nginx":               false,
		"docker logs -ft web":                     true,
		"docker logs -tf web":                     true,
		"docker logs -n5 web":                     false,
		"docker compose logs -ft":                 true,
		"docker-compose logs -tf web":             true,
		"kubectl logs -fc app pod":                true,
		"kubectl logs -cf pod":                    false, // container "f"
		"oc logs -f pod":                          true,
		"kubectl get pods -w":                     true,
		"kubectl apply -fx.yaml":                  false,
		"docker build -fDockerfile .":             false,
		"stern api":                               true, // stern follows by default
		"stern api --no-follow":                   false,
		"docker --context prod stats":             true,
		"docker -H tcp://h events":                true,
		"docker --context prod stats --no-stream": false,
		"timeout 10 tail -fn 5 app.log":           true,
	}
	for cmd, want := range cases {
		if got := ShouldStream(strings.Fields(cmd)); got != want {
			t.Errorf("ShouldStream(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// Nothing the hook rewrites may be something lx then streams: the agent
// would get raw output from a rewrite that promised a condensed view (and
// a watcher would be sent through lx only to be passed through). The
// commands are the hook↔filter contract table in internal/hook.
func TestSupportedNeverStreams(t *testing.T) {
	cmds := contractCommands(t)
	if len(cmds) < 200 {
		t.Fatalf("only %d contract commands", len(cmds))
	}
	checked := 0
	for _, cmd := range cmds {
		// Bare, and under each wrapper the hook looks through.
		for _, w := range []string{"", "uv run", "poetry run", "uvx", "env -u X", "timeout 5", "nice -n 5", "nohup", "time -p"} {
			argv := append(strings.Fields(w), strings.Fields(cmd)...)
			if !hook.Supported(argv) {
				continue
			}
			checked++
			if ShouldStream(argv) {
				t.Errorf("%q: the hook rewrites it but ShouldStream says stream", strings.Join(argv, " "))
			}
		}
	}
	if checked < 100 {
		t.Errorf("only %d supported commands checked", checked)
	}
}

// contractCommands reads the string literals of contractCommands from
// internal/hook/contract_test.go (test files cannot be imported).
func contractCommands(t *testing.T) []string {
	t.Helper()
	path := filepath.Join("..", "hook", "contract_test.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "contractCommands" || len(vs.Values) != 1 {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatalf("%s: contractCommands is not a composite literal", path)
		}
		for _, e := range lit.Elts {
			bl, ok := e.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				t.Fatalf("%s: contractCommands has a non-string element", path)
			}
			s, err := strconv.Unquote(bl.Value)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return false
	})
	return out
}
