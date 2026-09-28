package engine

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPeel(t *testing.T) {
	const none = "\x00nil"
	cases := []struct{ argv, inner, via string }{

		{"FOO=1 go test ./...", "go test ./...", "VAR=value"},
		{"FOO=1 BAR= _X9=a=b pytest -x", "pytest -x", "VAR=value"},
		{"FOO=1", none, ""},
		{"1FOO=x go test", none, ""},
		{"FOO-BAR=x go test", none, ""},
		{"=x go test", none, ""},

		{"env FOO=1 go test", "go test", "env"},
		{"/usr/bin/env FOO=1 BAR=2 go vet ./...", "go vet ./...", "env"},
		{"env go test", "go test", "env"},
		{"env -u CLAUDECODE -u AI_AGENT FORCE_COLOR=1 npx jest", "npx jest", "env"},
		{"env -uHOME --unset=PATH --unset GOFLAGS go build", "go build", "env"},
		{"env -- FOO=1 go test", "go test", "env"},
		{"env a.b=1 go test", "go test", "env"},
		{"env -i go test", none, ""},
		{"env - go test", none, ""},
		{"env -S 'go test' x", none, ""},
		{"env -C /tmp go test", none, ""},
		{"env --chdir=/tmp go test", none, ""},
		{"env -0", none, ""},
		{"env -u", none, ""},
		{"env", none, ""},
		{"env FOO=1", none, ""},
		{"env FOO=1 -x", none, ""},

		{"timeout 60 go test ./...", "go test ./...", "timeout"},
		{"timeout 1.5h cargo build", "cargo build", "timeout"},
		{"timeout .5 make", "make", "timeout"},
		{"timeout -s KILL 60 pytest", "pytest", "timeout"},
		{"timeout -sKILL -k 5 --preserve-status --foreground -v 5m go test", "go test", "timeout"},
		{"timeout --signal=TERM --kill-after=10s 60 go test", "go test", "timeout"},
		{"timeout -k5 60 go test", "go test", "timeout"},
		{"timeout -- 60 go test", "go test", "timeout"},
		{"gtimeout 60 go test", "go test", "timeout"},
		{"/opt/homebrew/bin/gtimeout -k 5 60 go test", "go test", "timeout"},
		{"gtimeout --bogus 5 go test", none, ""},
		{"timeout --bogus 5 go test", none, ""},
		{"timeout go test", none, ""},
		{"timeout 5ms go test", none, ""},
		{"timeout . go test", none, ""},
		{"timeout 5", none, ""},
		{"timeout -k x 5 go test", none, ""},
		{"timeout -s", none, ""},
		{"timeout 5 -x", none, ""},

		{"nice make", "make", "nice"},
		{"nice -n 10 make", "make", "nice"},
		{"nice -n10 make test", "make test", "nice"},
		{"nice -5 go test", "go test", "nice"},
		{"nice --5 go test", "go test", "nice"},
		{"nice --adjustment=3 go test", "go test", "nice"},
		{"nice --adjustment 3 go test", "go test", "nice"},
		{"nice -- go test", "go test", "nice"},
		{"nice -n x go test", none, ""},
		{"nice -n", none, ""},
		{"nice --help", none, ""},
		{"nice", none, ""},

		{"nohup go test ./...", "go test ./...", "nohup"},
		{"nohup -- go test", "go test", "nohup"},
		{"nohup --version", none, ""},
		{"nohup", none, ""},
		{"time go build", "go build", "time"},
		{"time -p go build", "go build", "time"},
		{"/usr/bin/time -p -- go build", "go build", "time"},
		{"time -v go build", none, ""},
		{"time -f %e go build", none, ""},
		{"time", none, ""},
		{"command git status", "git status", "command"},
		{"command -v go", none, ""},
		{"command -V go", none, ""},
		{"command -p go test", none, ""},
		{"command", none, ""},

		{"uv run pytest -x", "pytest -x", "uv run"},
		{"uv run --with ruff pytest", "pytest", "uv run"},
		{"uv run -w ruff --frozen pytest", "pytest", "uv run"},
		{"uv run --with=ruff --python 3.12 -q pytest", "pytest", "uv run"},
		{"uv run --package api --extra dev --group test pytest", "pytest", "uv run"},
		{"uv run --env-file .env --directory sub --project . pytest", "pytest", "uv run"},
		{"uv run --index-url https://x --extra-index-url https://y pytest", "pytest", "uv run"},
		{"uv run --exclude-newer 2024-01-01 pytest", "pytest", "uv run"},
		{"uv run --no-extra docs --prerelease allow --resolution lowest pytest", "pytest", "uv run"},
		{"uv run -- pytest --x", "pytest --x", "uv run"},
		{"uv run -m pytest", "pytest", "uv run"},
		{"uv run python -m pytest", "python -m pytest", "uv run"},
		{"uv run python", "python", "uv run"},
		{"uv run script.py", "script.py", "uv run"},
		{"uv tool run ruff check .", "ruff check .", "uv tool run"},
		{"uv tool run --from ruff ruff check", "ruff check", "uv tool run"},
		{"uvx ruff check .", "ruff check .", "uvx"},
		{"uvx ruff@0.6.1 check .", "ruff check .", "uvx"},
		{"uvx --from 'ruff==0.6' ruff check", "ruff check", "uvx"},
		{"uvx git+https://x@main check", "git+https://x@main check", "uvx"},
		{"poetry run mypy src", "mypy src", "poetry run"},
		{"poetry run -C sub pytest", "pytest", "poetry run"},
		{"pdm run -p app pytest", "pytest", "pdm run"},
		{"pipenv run pytest", "pytest", "pipenv run"},
		{"hatch run test:cov", "test:cov", "hatch run"},
		{"rye run pytest", "pytest", "rye run"},
		{"pipx run ruff check .", "ruff check .", "pipx run"},
		{"pipx run --spec ruff ruff check .", "ruff check .", "pipx run"},
		{"/home/u/.local/bin/uv run pytest", "pytest", "uv run"},
		{"uv.exe run pytest", "pytest", "uv run"},
		{"uv run", none, ""},
		{"uv run --with ruff", none, ""},
		{"uv run --", none, ""},
		{"uv run -- -x", none, ""},
		{"uv run - arg", none, ""},
		{"uv sync", none, ""},
		{"uv pip install x", none, ""},
		{"uv tool install ruff", none, ""},
		{"uv", none, ""},
		{"uvx", none, ""},
		{"poetry install", none, ""},
		{"poetry run", none, ""},
		{"pdm run --list", none, ""},
		{"pipx install ruff", none, ""},

		{"go test ./...", none, ""},
		{"sudo go test", none, ""},
		{"xargs go test", none, ""},
		{"watch git status", none, ""},
		{"", none, ""},
	}
	for _, tc := range cases {
		argv := fields(tc.argv)
		inner, via := Peel(argv)
		if tc.inner == none {
			if inner != nil || via != "" {
				t.Errorf("Peel(%q) = %q, %q; want nil", tc.argv, inner, via)
			}
			continue
		}
		if want := fields(tc.inner); !reflect.DeepEqual(inner, want) || via != tc.via {
			t.Errorf("Peel(%q) = %q, %q; want %q, %q", tc.argv, inner, via, want, tc.via)
		}
	}
	if len(cases) < 40 {
		t.Fatalf("only %d cases", len(cases))
	}
}

func fields(s string) []string {
	var out []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			break
		}
		if s[0] == '\'' {
			end := strings.IndexByte(s[1:], '\'')
			out = append(out, s[1:1+end])
			s = s[end+2:]
			continue
		}
		end := strings.IndexByte(s, ' ')
		if end < 0 {
			end = len(s)
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return out
}

func TestPeelIsPure(t *testing.T) {
	for _, s := range []string{"FOO=1 go test", "env -u X A=1 go test", "timeout -s KILL 5 make",
		"nice -n 3 make", "uvx ruff@0.6 check .", "uv run --with x pytest -x", "time -p go build"} {
		argv := fields(s)
		orig := slices.Clone(argv)
		inner, _ := Peel(argv)
		again, _ := Peel(argv)
		if !reflect.DeepEqual(argv, orig) {
			t.Errorf("Peel(%q) modified its argument: %q", s, argv)
		}
		if !reflect.DeepEqual(inner, again) {
			t.Errorf("Peel(%q) not deterministic", s)
		}
		if len(inner) == 0 || len(inner) >= len(argv) {
			t.Fatalf("Peel(%q) = %q", s, inner)
		}
		tail := argv[len(argv)-len(inner):]
		if !reflect.DeepEqual(inner[1:], tail[1:]) || !strings.HasPrefix(tail[0], inner[0]) {
			t.Errorf("Peel(%q) = %q is not a suffix of argv", s, inner)
		}
	}
}

type fakeFilter struct{ name, tool string }

func (f fakeFilter) Name() string                                { return f.name }
func (f fakeFilter) Match(c *Context) bool                       { return c.Name() == f.tool }
func (f fakeFilter) Apply(c *Context, out string) (string, bool) { return out, true }

func init() {
	Register(fakeFilter{"peel-test-inner", "lx-peel-test-tool"})
	Register(fakeFilter{"peel-test-outer", "lx-peel-test-wrapper"})
}

func TestResolve(t *testing.T) {
	c := &Context{Argv: fields("lx-peel-test-tool -x"), Exit: 3, Cwd: "/w", Home: "/h", Budget: 77}
	if f, fc := Resolve(c); f == nil || f.Name() != "peel-test-inner" || fc != c {
		t.Fatalf("direct match: %v %v", f, fc)
	}

	argv := fields("timeout 60 env -u X A=1 nice -n 5 lx-peel-test-tool -x")
	orig := slices.Clone(argv)
	c = &Context{Argv: argv, Exit: 3, Cwd: "/w", Home: "/h", Budget: 77}
	f, fc := Resolve(c)
	if f == nil || f.Name() != "peel-test-inner" {
		t.Fatalf("peeled match: %v", f)
	}
	want := Context{Argv: fields("lx-peel-test-tool -x"), Exit: 3, Cwd: "/w", Home: "/h", Budget: 77}
	if !reflect.DeepEqual(*fc, want) {
		t.Errorf("resolved context = %+v, want %+v", *fc, want)
	}
	if fc == c || !reflect.DeepEqual(c.Argv, orig) {
		t.Errorf("Resolve changed the caller's context: %q", c.Argv)
	}

	c = &Context{Argv: fields("lx-peel-test-wrapper run lx-peel-test-tool")}
	if f, fc := Resolve(c); f == nil || f.Name() != "peel-test-outer" || fc != c {
		t.Errorf("outer match: %v", f)
	}

	deep := "lx-peel-test-tool"
	for i := range MaxPeel + 1 {
		c = &Context{Argv: fields(deep)}
		if f, _ := Resolve(c); f == nil {
			t.Errorf("%d layers: no match for %q", i, deep)
		}
		deep = "nohup " + deep
	}
	c = &Context{Argv: fields(deep)}
	if f, fc := Resolve(c); f != nil || fc != c {
		t.Errorf("%d layers: matched %v", MaxPeel+1, f)
	}

	for _, s := range []string{"env -i lx-peel-test-tool", "sudo lx-peel-test-tool", "uv run nothing-here", ""} {
		c = &Context{Argv: fields(s)}
		if f, fc := Resolve(c); f != nil || fc != c {
			t.Errorf("Resolve(%q) = %v", s, f)
		}
	}
}

func TestMachineReadableAny(t *testing.T) {
	for s, want := range map[string]bool{
		"git status -s":                               true,
		"uv run git status -s":                        true,
		"env A=1 git rev-parse HEAD":                  true,
		"timeout 5 git log --format=%H":               true,
		"nice uv run git ls-files":                    true,
		"FOO=1 go vet -json ./...":                    true,
		"uv run pytest -x":                            false,
		"env A=1 git status":                          false,
		"poetry run mypy src":                         false,
		"env -i git status -s":                        false,
		"nohup nohup nohup nohup git status -s":       true,
		"nohup nohup nohup nohup nohup git status -s": false,
	} {
		if got := MachineReadableAny(&Context{Argv: fields(s)}); got != want {
			t.Errorf("MachineReadableAny(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestPeelHelpers(t *testing.T) {
	for s, want := range map[string]bool{"60": true, "1.5": true, ".5": true, "5.": true, "5s": true, "2d": true,
		"": false, ".": false, "s": false, "5ms": false, "1e3": false, "-5": false, "1.2.3": false, "inf": false} {
		if got := peelIsDuration(s); got != want {
			t.Errorf("peelIsDuration(%q) = %v", s, got)
		}
	}
	for s, want := range map[string]bool{"10": true, "-5": true, "+3": true, "": false, "-": false, "1a": false} {
		if got := peelIsInt(s); got != want {
			t.Errorf("peelIsInt(%q) = %v", s, got)
		}
	}
}

func FuzzPeel(f *testing.F) {
	for _, s := range []string{"env -u X A=1 go test", "timeout -s KILL 5 make", "uv run --with x pytest",
		"uvx ruff@1 check", "nice -n 3 time -p nohup command go", "FOO=1 env", "uv run -"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		argv := strings.Fields(s)
		orig := slices.Clone(argv)
		inner, via := Peel(argv)
		if !reflect.DeepEqual(argv, orig) {
			t.Fatalf("Peel modified argv")
		}
		if inner == nil {
			if via != "" {
				t.Fatalf("nil inner with via %q", via)
			}
			return
		}
		if len(inner) == 0 || len(inner) >= len(argv) || via == "" {
			t.Fatalf("Peel(%q) = %q, %q", argv, inner, via)
		}
		_ = MachineReadableAny(&Context{Argv: argv})
		_, _ = Resolve(&Context{Argv: argv})
	})
}
