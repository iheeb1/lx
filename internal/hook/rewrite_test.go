package hook

import (
	"reflect"
	"strings"
	"testing"
)

// same marks cases that must come back unchanged (ok=false).
const same = "\x00same"

func TestRewrite(t *testing.T) {
	cases := []struct{ in, want string }{
		// basics
		{`git status`, `lx git status`},
		{`git diff`, `lx git diff`},
		{`git log --oneline -20`, `lx git log --oneline -20`},
		{`   git status`, `   lx git status`},
		{`git status;`, `lx git status;`},
		{"git status\n", "lx git status\n"},
		{"ls -la\n\n", "lx ls -la\n\n"},
		{`/usr/bin/git status`, `lx /usr/bin/git status`},
		{`"git" status`, `lx "git" status`},
		{`\git status`, `lx \git status`},
		{`ls *.go`, `lx ls *.go`},
		{`ls ${HOME}`, `lx ls ${HOME}`},

		// compound commands
		{`cd dir && npm test`, `cd dir && lx npm test`},
		{`git status; git diff`, `lx git status; lx git diff`},
		{`git status || git diff`, `lx git status || lx git diff`},
		{`git status && rm -rf /tmp/x`, `lx git status && rm -rf /tmp/x`},
		{`git status && lx git diff`, `lx git status && lx git diff`},
		{`pytest -x; git status &`, `lx pytest -x; git status &`},
		{`npm test & git status`, `npm test & lx git status`},

		// quoting is preserved byte for byte
		{`echo "a && b"`, same},
		{`grep -rn 'x|y' .`, `lx grep -rn 'x|y' .`},
		{`git commit -m "fix: a && b; c | d"`, `lx git commit -m "fix: a && b; c | d"`},
		{`git commit -m 'it'"'"'s done'`, `lx git commit -m 'it'"'"'s done'`},
		{`git commit -m '$(not run)'`, `lx git commit -m '$(not run)'`},
		{`git log --grep="a|b" -- 'a b.txt'`, `lx git log --grep="a|b" -- 'a b.txt'`},
		{`git status # check && rm -rf /`, `lx git status # check && rm -rf /`},
		{"git log \\\n  --oneline", "lx git log \\\n  --oneline"},
		{`git\ status`, same},
		{`echo 'unterminated`, same},
		{`git commit -m "unterminated`, same},

		// env assignments and wrappers are peeled and kept
		{`FOO=1 go test ./... 2>&1 | tail -30`, `FOO=1 lx go test ./... 2>&1 | tail -30`},
		{`FOO=1 timeout 60 pytest -x`, `FOO=1 timeout 60 lx pytest -x`},
		{`FOO="a b" BAR='c' go vet ./...`, `FOO="a b" BAR='c' lx go vet ./...`},
		{`time go build ./...`, `time lx go build ./...`},
		{`time -p go build ./...`, `time -p lx go build ./...`},
		{`nice -n 10 make`, `nice -n 10 lx make`},
		{`nohup go test ./...`, `nohup lx go test ./...`},
		{`noglob ls *`, `noglob lx ls *`},
		{`command git status`, `command lx git status`},
		{`command -v git`, same},
		{`env FOO=1 go test ./...`, `env FOO=1 lx go test ./...`},
		{`env -i go test ./...`, same},
		{`timeout 5m go test ./...`, `timeout 5m lx go test ./...`},
		{`timeout -s KILL 60 go test ./...`, `timeout -s KILL 60 lx go test ./...`},
		{`timeout --foreground 1.5h cargo build`, `timeout --foreground 1.5h lx cargo build`},
		{`timeout go test`, same},
		{`FOO=bar`, same},
		{`sudo apt install foo`, same},

		// idempotency and opt-outs
		{`lx git status`, same},
		{`/usr/local/bin/lx git status`, same},
		{`lx --raw go test ./...`, same},
		{`LX_RAW=1 git status`, same},
		{`LX_OFF=1 go test ./...`, same},
		{`env LX_RAW=1 git status`, same},

		// pipelines
		{`ls -la | head -20`, `lx ls -la | head -20`},
		{`go test ./... | tail -n 30`, `lx go test ./... | tail -n 30`},
		{`git status | cat`, `lx git status | cat`},
		{`git status 2>&1 | head`, `lx git status 2>&1 | head`},
		{`git status |& tail -5`, `lx git status |& tail -5`},
		{`ls | wc -l`, same},
		{`cat file | head`, same},
		{`git log | grep fix`, same},
		{`git log | tail -f`, same},
		{`git log | tail -F`, same},
		{`git log | head -5 > out.txt`, same},
		{`git log | sort | head`, same},

		// redirects
		{`git status 2>&1`, `lx git status 2>&1`},
		{`git status > out.txt`, same},
		{`git status >> out.txt`, same},
		{`go test ./... 2>/dev/null`, same},
		{`go test ./... &> log`, same},
		{`go test ./... > /dev/null 2>&1`, same},
		{`go test ./... 1>&2`, same},
		{`go test ./... < input`, same},
		{`git status >& /tmp/evil`, same},

		// background
		{`npm test &`, same},
		{`pytest -x && git status &`, same},
		{`git status & rm -rf /tmp/x`, same},

		// constructs lx will not reason about
		{"cat <<EOF\nhello\nEOF", same},
		{`git status <<< "x"`, same},
		{`git log --format=$(cat f)`, same},
		{`git log --pretty=$(rm -rf /tmp/x)`, same},
		{"git log --pretty=`rm -rf /tmp/x`", same},
		{`git commit -m "$(cat msg)"`, same},
		{`echo $(git status)`, same},
		{`ls ${x:-$(pwd)}`, same},
		{`diff <(git show a) <(git show b)`, same},
		{`ls >(cat)`, same},
		{`(cd x && make)`, same},
		{`{ git status; }`, same},
		{`if true; then git status; fi`, same},
		{`for f in a b; do ls $f; done`, same},
		{`while true; do git status; done`, same},
		{`[[ -f x ]] && npm test`, same},
		{`! git diff --quiet`, same},
		{`a=(1 2) && git status`, same},
		{"git status\ngit diff", same},
		{"git status\nrm -rf /tmp/x", same},
		{`$GIT status`, same},
		{`; git status`, same},
		{`git status &&`, same},
		{`git status |`, same},
		{``, same},
		{`   `, same},

		// git exact-bytes forms
		{`git log --pretty=format:%h`, same},
		{`git log --pretty=tformat:%h`, same},
		{`git log --pretty=%h`, same},
		{`git log --pretty=oneline`, `lx git log --pretty=oneline`},
		{`git log --format=%H -1`, same},
		{`git status --porcelain`, same},
		{`git status --porcelain=v2`, same},
		{`git status -z`, same},
		{`git diff --name-only`, same},
		{`git diff --name-status HEAD~1`, same},
		{`git show HEAD:README.md`, same},
		{`git show HEAD --stat`, `lx git show HEAD --stat`},
		{`git rev-parse HEAD`, same},
		{`git ls-files`, same},
		{`git config user.email`, same},
		{`git -C /tmp/repo status`, `lx git -C /tmp/repo status`},
		{`git --no-pager log -5`, `lx git --no-pager log -5`},
		{`git rebase -i HEAD~3`, same},
		{`git remote -v`, `lx git remote -v`},
		{`git remote add origin x`, same},
		{`git blame --porcelain f.go`, same},
		{`git log --follow f.go`, `lx git log --follow f.go`},

		// package managers and runners
		{`npm run dev`, same},
		{`npm run start`, same},
		{`npm run serve:prod`, same},
		{`npm run build`, `lx npm run build`},
		{`npm run test:unit`, `lx npm run test:unit`},
		{`npm run test:watch`, same},
		{`npm run type-check`, `lx npm run type-check`},
		{`npm test -- --watch`, same},
		{`npm i`, `lx npm i`},
		{`npm ls --json`, same},
		{`pnpm lint`, `lx pnpm lint`},
		{`pnpm --filter web build`, `lx pnpm --filter web build`},
		{`yarn start`, same},
		{`yarn`, `lx yarn`},
		{`yarn workspace app test`, `lx yarn workspace app test`},
		{`npx tsc --noEmit`, `lx npx tsc --noEmit`},
		{`npx -y vitest run`, `lx npx -y vitest run`},
		{`npx vitest watch`, same},
		{`npx prettier --write .`, same},
		{`npx prettier --check .`, `lx npx prettier --check .`},
		{`npx create-react-app x`, same},
		{`pnpm dlx tsc -p .`, `lx pnpm dlx tsc -p .`},
		{`bunx jest --watch`, same},
		{`jest --json`, same},
		{`vitest --watch`, same},
		{`tsc -w`, same},
		{`tsc --noEmit`, `lx tsc --noEmit`},
		{`eslint -f json .`, same},
		{`eslint .`, `lx eslint .`},

		// go / cargo / python
		{`go run main.go`, same},
		{`go test -json ./...`, same},
		{`go test -run TestX -count=1 ./pkg/...`, `lx go test -run TestX -count=1 ./pkg/...`},
		{`go mod tidy`, `lx go mod tidy`},
		{`go mod edit -json`, same},
		{`cargo test`, `lx cargo test`},
		{`cargo +nightly clippy`, `lx cargo +nightly clippy`},
		{`cargo build --message-format=json`, same},
		{`cargo run`, same},
		{`pytest -x`, `lx pytest -x`},
		{`pytest --pdb`, same},
		{`python -m pytest -x`, `lx python -m pytest -x`},
		{`python3.12 -m pytest`, `lx python3.12 -m pytest`},
		{`python3 script.py`, same},
		{`python3 -c 'print(1)'`, same},
		{`pip install -r requirements.txt`, `lx pip install -r requirements.txt`},
		{`pip freeze`, same},
		{`mypy src`, `lx mypy src`},
		{`ruff check .`, `lx ruff check .`},
		{`ruff check --output-format json .`, same},
		{`ruff format .`, same},

		// build tools
		{`make`, `lx make`},
		{`make test`, `lx make test`},
		{`make dev`, same},
		{`make -C sub -j8 lint`, `lx make -C sub -j8 lint`},
		{`./gradlew test`, `lx ./gradlew test`},
		{`./gradlew bootRun`, same},
		{`./gradlew test --continuous`, same},
		{`mvn -q test`, `lx mvn -q test`},
		{`mvn spring-boot:run`, same},

		// listing and search
		{`find . -name '*.go'`, `lx find . -name '*.go'`},
		{`find . -name '*.go' -exec rm {} \;`, same},
		{`find . -print0`, same},
		{`grep -rl foo .`, same},
		{`grep -c foo file`, same},
		{`grep -A3 -e lint src`, `lx grep -A3 -e lint src`},
		{`rg --json foo`, same},
		{`rg -n foo`, `lx rg -n foo`},
		{`tree -L 2`, `lx tree -L 2`},
		{`tree -J`, same},
		{`du -sh *`, `lx du -sh *`},
		{`cat file.txt`, same},

		// containers, clusters, logs, network
		{`docker logs -f x`, same},
		{`docker logs --tail 50 x`, `lx docker logs --tail 50 x`},
		{`docker compose -f dc.yml logs web`, `lx docker compose -f dc.yml logs web`},
		{`docker compose logs -f web`, same},
		{`docker run -it ubuntu`, same},
		{`docker ps --format '{{.Names}}'`, same},
		{`docker build -f Dockerfile .`, `lx docker build -f Dockerfile .`},
		{`kubectl get pods -o json`, same},
		{`kubectl get pods -oyaml`, same},
		{`kubectl get pods -o wide`, `lx kubectl get pods -o wide`},
		{`kubectl -n prod logs -f web`, same},
		{`kubectl get pods -w`, same},
		{`journalctl -fu nginx`, same},
		{`journalctl -u nginx -n 100`, `lx journalctl -u nginx -n 100`},
		{`curl -sSL https://example.com`, `lx curl -sSL https://example.com`},
		{`curl -I https://example.com`, `lx curl -I https://example.com`},
		{`curl -o out.tgz https://example.com`, same},
		{`curl -sSLo out https://example.com`, same},
		{`curl -w '%{http_code}' https://example.com`, same},

		// the rest of the table
		{`terraform plan`, `lx terraform plan`},
		{`terraform plan -json`, same},
		{`terraform apply`, same},
		{`brew install jq`, `lx brew install jq`},
		{`swift build`, `lx swift build`},
		{`dotnet test`, `lx dotnet test`},
		{`flutter test --machine`, same},
		{`composer install`, `lx composer install`},
		{`bundle install`, `lx bundle install`},
		{`bundle exec rspec spec/a_spec.rb`, `lx bundle exec rspec spec/a_spec.rb`},
		{`rspec`, `lx rspec`},
		{`phpunit`, `lx phpunit`},
	}
	for _, c := range cases {
		got, ok := Rewrite(c.in)
		want, wantOK := c.want, true
		if c.want == same {
			want, wantOK = c.in, false
		}
		if got != want || ok != wantOK {
			t.Errorf("Rewrite(%q)\n got  %q ok=%v\n want %q ok=%v (reason: %s)",
				c.in, got, ok, want, wantOK, Inspect(c.in).Reason)
		}
	}
	if len(cases) < 60 {
		t.Fatalf("only %d cases", len(cases))
	}
}

// Rewriting must only ever insert "lx " — every original byte survives in
// order — and must be idempotent.
func TestRewriteInvariants(t *testing.T) {
	inputs := []string{
		`git status`, `cd a && npm test && git diff | head`, `FOO=1 timeout 60 pytest -x`,
		`git commit -m "a; b" && make test || go vet ./...`, "git status\n", `git status; git diff; ls`,
	}
	for _, in := range inputs {
		out, ok := Rewrite(in)
		if !ok {
			t.Fatalf("%q not rewritten", in)
		}
		if strings.ReplaceAll(out, "lx ", "") != strings.ReplaceAll(in, "lx ", "") {
			t.Errorf("%q → %q changed more than inserting lx", in, out)
		}
		again, ok2 := Rewrite(out)
		if ok2 || again != out {
			t.Errorf("not idempotent: %q → %q → %q (ok=%v)", in, out, again, ok2)
		}
	}
}

func TestInspect(t *testing.T) {
	in := Inspect(`FOO=1 go test ./... && git status | head && echo done`)
	want := [][]string{{"go", "test", "./..."}, {"git", "status"}}
	if !reflect.DeepEqual(in.Targets, want) {
		t.Errorf("Targets = %q, want %q", in.Targets, want)
	}
	if len(in.Commands) != 4 {
		t.Errorf("Commands = %q", in.Commands)
	}
	if in.AlreadyLx {
		t.Error("AlreadyLx")
	}
	if !Inspect(`cd x && lx go test`).AlreadyLx {
		t.Error("lx not detected")
	}
	if r := Inspect("echo $(date)").Reason; r != "command substitution" {
		t.Errorf("reason = %q", r)
	}
	// Commands are best effort even when the string is not rewritable.
	if c := Inspect("cat <<EOF\nx\nEOF").Commands; len(c) == 0 || c[0][0] != "cat" {
		t.Errorf("Commands for heredoc = %q", c)
	}
}

func TestSupported(t *testing.T) {
	yes := [][]string{
		{"git", "status"}, {"/opt/homebrew/bin/git", "diff"}, {"go", "vet", "./..."},
		{"npm", "run", "lint:fix"}, {"pnpm", "exec", "eslint", "."}, {"npm", "exec", "--", "tsc"},
		{"npx", "tsc@5.4", "--noEmit"}, {"npx", "playwright", "test"}, {"npx", "next", "build"},
		{"npx", "vite", "build"}, {"python", "-u", "-m", "pip", "install", "x"}, {"pip3.11", "list"},
		{"python", "-munittest"}, {"golangci-lint", "run", "./..."}, {"ls"}, {"docker", "compose", "ps"},
		{"docker-compose", "build"}, {"docker", "image", "ls"}, {"kubectl", "describe", "pod", "x"},
		{"kubectl", "top", "nodes"}, {"brew", "outdated"}, {"terraform", "-chdir=infra", "init"},
		{"dart", "analyze"}, {"mvnw", "verify"}, {"gradlew", "build"}, {"jest", "-i"},
		{"rg", "-e", "x", "-A2"}, {"grep", "-rniE", "a|b", "."},
	}
	no := [][]string{
		{}, {"git"}, {"git", "--version"}, {"git", "stash", "-p"}, {"git", "commit", "-pm", "x"},
		{"go"}, {"go", "env"}, {"npm"}, {"npm", "publish"}, {"npm", "run"}, {"npm", "build"},
		{"npm", "update", "-i"}, {"npx"}, {"npx", "-c", "tsc"}, {"vite"}, {"vite", "build", "-w"},
		{"next", "dev"}, {"playwright", "test", "--ui"}, {"python"}, {"python", "-m", "http.server"},
		{"pip", "show", "x"}, {"pip", "list", "--format=json"}, {"mypy", "-O", "json", "."},
		{"golangci-lint", "run", "--out-format", "json"}, {"make", "run-server"}, {"make", "-p"},
		{"docker"}, {"docker", "exec", "x", "ls"}, {"docker", "compose", "up"},
		{"kubectl", "exec", "x"}, {"kubectl", "get", "po", "--output=jsonpath={.items}"},
		{"journalctl", "--follow"}, {"curl", "-K", "cfg"}, {"brew", "uninstall", "x"},
		{"swift"}, {"swift", "run"}, {"dotnet", "watch", "test"}, {"rspec", "-f", "json"},
		{"tail", "-f", "log"}, {"head", "x"}, {"cat", "x"}, {"sed", "-n", "1p"}, {"vim"},
		{"du", "-0"}, {"tree", "-aJ"}, {"rg", "-l", "x"}, {"grep", "-rl", "x"}, {"grep", "-Z", "x"},
	}
	for _, a := range yes {
		if !Supported(a) {
			t.Errorf("Supported(%q) = false, want true", a)
		}
	}
	for _, a := range no {
		if Supported(a) {
			t.Errorf("Supported(%q) = true, want false", a)
		}
	}
}

func TestScriptOK(t *testing.T) {
	for s, want := range map[string]bool{
		"test": true, "test:unit": true, "build:prod": true, "lint": true, "typecheck": true,
		"type-check": true, "check": true, "compile": true, "tests": true,
		"dev": false, "start": false, "serve": false, "watch": false, "preview": false,
		"test:watch": false, "build-dev": false, "deploy": false, "": false, "storybook": false,
	} {
		if got := scriptOK(s); got != want {
			t.Errorf("scriptOK(%q) = %v", s, got)
		}
	}
}

func TestLexOffsets(t *testing.T) {
	src := `A="x y" git  log 2>&1 | head`
	l := lex(src)
	if l.broken || len(l.unsafe) > 0 {
		t.Fatalf("lex: %+v", l)
	}
	var got []string
	for _, tk := range l.toks {
		if src[tk.start:tk.end] != tk.text {
			t.Errorf("token %q has wrong offsets", tk.text)
		}
		got = append(got, tk.text)
	}
	want := []string{`A="x y"`, "git", "log", "2>&", "1", "|", "head"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokens = %q, want %q", got, want)
	}
	if l.toks[0].val != "A=x y" {
		t.Errorf("value = %q", l.toks[0].val)
	}
}

func FuzzRewrite(f *testing.F) {
	for _, s := range []string{`git status`, `a && b | c`, `"x`, `$'a\'b' git log`, "x\\\ny", `${a`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out, ok := Rewrite(s)
		if !ok {
			if out != s {
				t.Fatalf("ok=false but output changed: %q → %q", s, out)
			}
			return
		}
		if strings.ReplaceAll(out, "lx ", "") != strings.ReplaceAll(s, "lx ", "") {
			t.Fatalf("%q → %q", s, out)
		}
	})
}
