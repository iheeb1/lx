package hook_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/hook"
)

var contractCommands = []string{

	"git status", "git status -v", "git status -s", "git status -sb", "git status --porcelain", "git status -z",
	"git log", "git log --stat", "git log --oneline -20", "git log --pretty=oneline", "git log --format=%H",
	"git log --pretty=format:%h", "git log --follow f.go", "git -C /repo -c color.ui=always --no-pager log -p -n 3",
	"/usr/local/bin/git log --stat", "git --no-pager log -5",
	"git show HEAD", "git show HEAD --stat", "git show HEAD:README.md",
	"git diff", "git diff --cached -- src/", "git diff --name-only", "git diff --name-status HEAD~1", "git diff --shortstat",
	"git stash list", "git stash show -p", "git stash -p",
	"git blame gin.go", "git blame --porcelain gin.go", "git blame -p gin.go", "git blame --line-porcelain gin.go",
	"git branch -a", "git branch -r --sort=-committerdate", "git branch -vv", "git branch -av", "git branch -d old",
	"git branch -D old", "git branch -m old new", "git branch new-feature",
	"git push origin main", "git pull --rebase", "git fetch --all --prune", "git clone https://example.com/x.git",
	"git merge feature", "git merge-base a b", "git rebase -i HEAD~3", "git rebase main",
	"git commit -m x", "git commit -pm x",
	"git tag", "git tag -n", "git tag -n5 -l v1*", "git tag -a v1 -m x", "git tag -d v1",
	"git remote -v", "git remote add origin x", "git remote show origin",
	"git reflog", "git reflog show feature", "git reflog expire --all",
	"git worktree list", "git worktree list --porcelain", "git worktree list -z", "git worktree add ../x",
	"git shortlog -sn", "git shortlog -sn -z", "git cherry -v", "git ls-remote origin", "git show-branch",
	"git submodule", "git submodule status", "git submodule summary", "git submodule update --init",
	"git clean -n -d", "git rm -r --cached dist", "git add -A", "git notes list",
	"git rev-parse HEAD", "git ls-files", "git config user.email", "git cat-file -p HEAD", "git --version", "git",
	"gitk", "hg log",

	"git grep -n foo", "git grep -n x", "git grep foo", "git -C sub grep x", "git --no-pager grep -n x", "git grep -z foo",
	"git grep -q foo", "git grep -O foo", "git grep --open-files-in-pager foo", "git grep --heading x",
	"git grep -p x", "git grep -c x", "git grep -l x", "git grep --name-only x", "git grep -W x", "git grep -e x -e y",
	"git log --grep x",
	"grep -rn x .", "/usr/bin/grep -n x a.go", "egrep -r a|b src", "fgrep -rn a.b .", "grep -A3 -e lint src",
	"grep -rniE a|b .", "grep -rn --include=*.go x .", "grep -A 3 -B2 -e x -e y f", "grep --context 3 x f",
	"grep -c x f", "grep -l x f", "grep -rl foo .", "grep -rlZ x .", "grep -q x f", "grep -Z x", "grep -b x f",
	"grep --null-data x f", "grep --help", "grep -V", "grep -F a.b f", "rg -F a.b", "git grep -F x", "ls -F",
	"rg x", "rg -n foo", "rg -n -C2 x src", "rg --vimgrep x", "rg -e x -A2", "rg -A3 -e x --glob *.ts",
	"rg --files", "rg --files src", "rg -l x", "rg --count x", "rg --json x", "rg --heading x", "rg -p x",
	"rg --stats x", "rg --type-list", "rg -h", "rg --files --null",

	"ls", "ls -la", "/bin/ls -l", "ls -R", "ls --help", "ls --zero",
	"find . -type f", "find . -name *.go", "/usr/bin/find src", "find . -print0", "find . -exec grep x {} ;",
	"find . -printf %p", "find . -ls", "find . -name x -delete", "fd -e go", "fd -x rm",
	"du -sh *", "du -0 .", "du --time -sh", "tree", "tree -L 2", "tree -a -I node_modules -L 3", "tree -J",
	"tree -aJ", "tree -C", "tree -h", "tree -X", "tree --du",
	"cat a.go", "cat file.txt", "head -n 5 a.go", "tail -n 5 a.go", "tail -f app.log", "bat -p a.go",
	"sed -n 1p f", "less f", "lsof -i",

	"go test ./...", "go test -v ./...", "go test -run TestX -count=1 ./pkg/...", "/usr/local/go/bin/go test -v ./...",
	"go -C sub test ./...", "go test -json ./...", "go test -bench=. ./...", "go test -fuzz=FuzzX .", "go test -x .",
	"go test -c ./pkg", "go build ./...", "go build -x ./...", "go build -json ./...", "go vet ./...",
	"go vet -json ./...", "go install example.com/cmd@latest", "go get -u ./...",
	"go mod tidy", "go mod download", "go mod vendor", "go mod graph", "go mod why x", "go mod edit -json",
	"go list ./...", "go list -m all", "go list -json ./...", "go run main.go", "go run .", "go env", "go version",
	"go", "gotip test ./...", "gotip build ./...", "go1.22.3 test .", "go1.23rc1 vet ./...", "gotip run .",
	"golangci-lint run ./...", "golangci-lint run --out-format json", "gofmt -l .",

	"jest", "jest -i", "jest --ci", "npx jest --ci", "npx jest@29 test/a.test.js", "./node_modules/.bin/jest --coverage",
	"pnpm exec jest", "yarn jest --ci", "bunx jest", "jest --watch", "jest --watchAll", "jest --json",
	"jest --listTests", "jest --showConfig", "bunx jest --watch",
	"vitest", "vitest run", "npx vitest run", "npx -y vitest run", "npx vitest run --reporter=verbose", "vitest watch",
	"vitest --watch", "vitest run --reporter=junit", "vitest list", "npx vitest watch",
	"mocha", "npx mocha --reporter spec test/", "mocha --reporter tap", "mocha -R json", "mocha --watch",
	"npm test", "npm t", "npm run test", "npm run test:unit", "npm run-script test", "npm --prefix app test",
	"npm test -- --watch", "npm run test:watch", "yarn test", "yarn run test", "pnpm test", "pnpm run test:e2e",
	"bun test", "bun run test", "yarn workspace web test", "pnpm --filter web test", "npm -w web test",
	"npm run build", "npm run type-check", "npm run lint", "pnpm lint", "pnpm --filter web build",
	"npm run dev", "npm run start", "npm run serve:prod", "yarn start", "npm i", "npm install", "npm ci",
	"npm i -D typescript", "npm uninstall codecov", "npm update", "npm update -i", "npm install --json",
	"pnpm install", "yarn", "yarn --frozen-lockfile", "bun install", "bun add zod",
	"npm ls", "npm ls --all", "npm ls --json", "npm ls --parseable", "pnpm ls --depth 2", "yarn list --depth=0",
	"npm audit", "npm audit fix", "npm audit --json", "pnpm audit", "npm outdated", "npm publish", "npm",
	"tsc", "tsc --noEmit", "tsc -w", "tsc --watch", "tsc --version", "tsc --showConfig", "npx tsc --noEmit",
	"npx tsc@5.4 --noEmit", "pnpm dlx tsc -p .", "npx -c tsc", "vue-tsc --noEmit",
	"eslint .", "npx eslint src --ext .ts", "eslint -f json .", "npx eslint -f json .", "eslint --format unix .",
	"prettier --check .", "npx prettier --check .", "npx prettier --write .",
	"next build", "next dev", "npx next build", "vite build", "vite", "npx vite build",
	"npx playwright test", "playwright test --ui", "npx create-react-app x",
	"node examples/search/index.js", "node server.js", "node", "node --test", "react-scripts test",
	"react-scripts test --watchAll=false", "craco test",

	"pytest", "pytest -x", "pytest --pdb", "pytest --collect-only", "pytest --version", "py.test -q",
	"/home/user/venv/bin/pytest -x tests", "python -m pytest -x", "python3 -m pytest", "python3.12 -m pytest",
	"python3.12 -u -m pytest tests/", "python -m unittest", "python -munittest", "python -m http.server",
	"python", "python3 script.py", "python3 -c print(1)",
	"pip install -r requirements.txt", "pip install requests", "pip3 install -r requirements.txt", "pip3.11 list",
	"pip list", "pip list --outdated", "pip list --format=json", "pip show requests", "pip freeze",
	"pip uninstall -y flask", "python -u -m pip install x",
	"mypy src", "python -m mypy --strict .", "mypy -O json .", "mypy --version",
	"ruff check .", "ruff check --output-format concise src", "ruff check --output-format json .", "ruff format .",
	"ruff check --watch", "ruff",

	"uv run pytest -x", "uv run pytest -k x", "uv run --with ruff pytest", "uv run --with pytest-xdist --frozen pytest -n 4",
	"uv run python -m pytest", "uv run mypy .", "uv run ruff check .", "uv run python", "uv run python script.py",
	"uv run script.py", "uv run pytest --pdb", "uv run git status", "uv run go test ./...", "uv sync", "uv pip install x",
	"uv tool run ruff check .", "uvx ruff check .", "uvx ruff@0.6 check .", "uvx ruff check --watch", "uvx pytest",
	"uvx --from ruff ruff check", "poetry run pytest", "poetry run mypy src", "poetry run pytest --pdb",
	"poetry run jest", "poetry run jest --watch", "poetry install", "pdm run pytest", "pipenv run python -m pytest",
	"hatch run test:cov", "rye run pytest", "pipx run ruff check .", "pipx install x",

	"env FOO=1 go test ./...", "env -u CLAUDECODE go test ./...", "env -i go test ./...", "env -S go test",
	"timeout 60 pytest -x", "timeout -s KILL 60 go test ./...", "timeout --bogus 5 go test", "timeout go test",
	"nice -n 10 make", "nohup go test ./...", "time go build ./...", "time -p go build ./...", "command git status",
	"command -v git", "/usr/bin/env FOO=1 git status", "env FOO=1 git status -s", "timeout 5 git log --format=%H",
	"sudo apt install foo", "env TZ=UTC npx jest", "env -u CLAUDECODE -u AI_AGENT FORCE_COLOR=1 npx jest",

	"make", "make test", "make dev", "make -C sub -j8 lint", "make -n", "make -p", "make run-server", "gmake all",
	"cmake --build build", "ninja", "ninja -C build", "gcc -Wall x.c -o x", "cc -c x.c",
	"cargo test", "cargo build", "cargo +nightly clippy", "cargo build --message-format=json", "cargo run",
	"cargo check", "cargo fmt", "cargo test -- --nocapture",
	"./gradlew test", "gradle build", "./gradlew bootRun", "./gradlew test --continuous", "gradle tasks",
	"mvn -q test", "mvn test", "./mvnw -B package", "mvn spring-boot:run", "mvn dependency:tree", "mvnw verify",

	"docker ps", "docker ps -a --no-trunc", "docker ps -q", "docker ps --format {{.Names}}", "docker images",
	"docker image ls", "docker compose ps", "docker-compose ps", "docker build -f Dockerfile .",
	"docker buildx build --platform linux/amd64 .", "docker compose build api", "docker pull node:20",
	"docker logs web", "docker logs --tail 50 x", "docker logs -f x", "docker compose -f dc.yml logs web",
	"docker compose logs -f web", "docker compose up", "docker run -it ubuntu", "docker run --rm alpine",
	"docker exec x ls", "docker inspect api",
	"kubectl get pods", "kubectl get pods -o wide", "kubectl get pods -o json", "kubectl get pods -oyaml",
	"kubectl get pods -w", "kubectl get events -A", "kubectl describe pod x", "kubectl top nodes",
	"kubectl -n prod logs web", "kubectl -n prod logs -f web", "kubectl exec x", "kubectl apply -f x.yaml",
	"journalctl -u nginx -n 100", "journalctl -fu nginx", "journalctl -o json", "journalctl --follow",
	"journalctl -F _SYSTEMD_UNIT",
	"curl -sSL https://example.com", "curl -I https://example.com", "curl -o out.tgz https://example.com",
	"curl -w %{http_code} https://example.com", "wget -qO- https://x", "http GET https://x", "jq . a.json",

	"docker ps -aq", "docker image ls -q", "docker compose ps -q", "du --inodes .", "ls -D", "pytest --co -q",
	"pytest --fixtures", "jest --clearCache", "mocha --dry-run", "eslint -o r.txt .", "ruff check --diff",
	"ruff check --statistics", "cargo test --no-run", "cargo test -- --list", "make --dry-run", "make -np",
	"gradle dependencies", "./gradlew :app:dependencies", "mvn -v", "mvn help:effective-pom", "go test -list .",
	"go build -n", "pip install --dry-run x", "grep -rn x . --help", "tsc --listFiles", "vitest bench",

	"ninja -t targets", "ninja -n", "cmake -S . -B build", "fdfind -t d", "fd -0", "vue-tsc --watch", "tsgo --noEmit",
	"ninja serve", "ninja -C build -j 8 all", "cmake --build build --target run", "cmake --build build --target test -j 8",

	"nice -n 5 uv run pytest -x", "uv run env FOO=1 pytest", "timeout 60 poetry run mypy src",
	"env -u X uv run pytest", "nohup nohup nohup nohup go test ./...", "nohup nohup nohup nohup nohup go test ./...",
	"env FOO=1 timeout 5 git status", "uv run git grep -n x", "uv run git status -s", "env FOO=1 cat a.go",
	"timeout 30 tail -f app.log", "env FOO=1 jest --watch", "uv run lx pytest", "uvx lx git status",
	"env -u X LX_RAW=1 go test ./...", "uv run env LX_OFF=1 pytest", "env -u X LX_RAW=0 go test ./...",
	"gtimeout 60 go test ./...", "git stash -p show", "git stash --patch", "git stash push -p",
	"python -i -m pytest", "uv run python -ui -m pytest", "python -I -m pytest", "python3 -u -m pytest -x",

	"terraform plan", "terraform init", "terraform plan -json", "terraform apply", "brew install jq", "brew outdated",
	"brew uninstall x", "swift build", "swift test", "swift run", "dotnet test", "dotnet build", "dotnet watch test",
	"flutter test", "flutter test --machine", "dart analyze", "composer install", "bundle install",
	"bundle exec rspec spec/a_spec.rb", "rspec", "rspec -f json", "phpunit", "vim", "ssh host",

	"just test", "just lint", "task test", "task lint test", "mise run test", "turbo run test",
	"turbo run build test lint", "nx test app", "nx run app:test", "nx run-many -t test lint", "npx nx test app",
	"rake test", "rake spec", "rake lint", "deno test", "deno check main.ts", "deno lint", "deno task test",
	"cargo nextest run", "composer test", "composer run-script typecheck", "npm run testonly",
	"just dev", "just --list", "task --watch test", "mise run", "turbo run dev", "turbo run test --dry-run",
	"nx serve app", "nx graph", "rake -T", "deno test --watch", "deno task dev", "cargo nextest list",
	"composer run-script --list", "sh scripts/ci.sh",

	"gh run view 42 --log-failed", "gh run view 42 --log --job 7", "gh -R o/r run view 42 --log", "gh run view 42",
	"gh run view --job 7", "gh run watch 42 --exit-status", "gh pr checks 12", "gh pr checks", "gh api repos/o/r/actions/jobs/7/logs",
	"gh run view 42 --web", "gh run view 42 --json jobs", "gh run view 42 --log -q .", "gh pr checks --watch",
	"gh pr checks --json name,state", "gh run list", "gh pr view 12", "gh api repos/o/r/actions/runs/42",
}

var genericOnly = map[string]string{
	"brew install jq":                  "no brew filter: download and pour progress is what the generic engine removes",
	"brew outdated":                    "no brew filter: a short table, passed through unless it is long",
	"bundle install":                   "no Ruby filter yet: install chatter folded by the generic engine",
	"bundle exec rspec spec/a_spec.rb": "no Ruby filter yet: failures are error lines the guard keeps",
	"rspec":                            "no Ruby filter yet: failures are error lines the guard keeps",
	"composer install":                 "no PHP filter yet: install chatter folded by the generic engine",
	"phpunit":                          "no PHP filter yet: failures are error lines the guard keeps",
	"dart analyze":                     "no Dart filter yet: diagnostics are error lines the guard keeps",
	"flutter test":                     "no Dart filter yet: failures are error lines the guard keeps",
	"dotnet build":                     "no .NET filter yet: diagnostics are error lines the guard keeps",
	"dotnet test":                      "no .NET filter yet: failures are error lines the guard keeps",
	"swift build":                      "no Swift filter yet: diagnostics are error lines the guard keeps",
	"swift test":                       "no Swift filter yet: failures are error lines the guard keeps",
	"terraform init":                   "no Terraform filter yet: provider download chatter folded by the generic engine",
	"terraform plan":                   "no Terraform filter yet: repeated resource blocks folded, errors kept by the guard",
	"golangci-lint run ./...":          "no golangci-lint filter yet: issues are file:line diagnostics the guard keeps",
	"npx playwright test":              "no Playwright filter yet: failures are error lines the guard keeps",
	"prettier --check .":               "no prettier filter: one line per unformatted file, folded only when repetitive",
	"npx prettier --check .":           "no prettier filter: one line per unformatted file, folded only when repetitive",
	"git branch -d old":                "branch writes print a line or two: below the small-output threshold, passed through",
	"git branch -D old":                "branch writes print a line or two: below the small-output threshold, passed through",
	"git branch -m old new":            "branch writes print nothing or an error: passed through",
	"git tag -d v1":                    "prints one line: passed through",
	"git rebase main":                  "progress and conflict hints: the generic engine keeps the CONFLICT and hint lines",
	"git reflog expire --all":          "prints nothing or an error: passed through",
}

var runnerOnly = map[string]string{
	"just test":                     "just recipe: the report it prints is detected (go test, jest, pytest …) or condensed by the generic engine",
	"just lint":                     "just recipe: eslint/tsc reports are detected",
	"task test":                     "go-task task: the report it prints is detected or condensed by the generic engine",
	"task lint test":                "go-task tasks: each report is detected or condensed by the generic engine",
	"mise run test":                 "mise task: the report it prints is detected or condensed by the generic engine",
	"turbo run test":                "turbo prefixes each line with pkg:task:; the monorepo detector regroups the lines by task and hands each task's output to its tool's filter",
	"turbo run build test lint":     "turbo prefixes each line with pkg:task:; the monorepo detector regroups the lines by task and hands each task's output to its tool's filter",
	"nx test app":                   "nx target: the executor's report (jest, vitest …) is detected",
	"nx run app:test":               "nx target: the executor's report is detected",
	"nx run-many -t test lint":      "nx targets: the executors' reports are detected or condensed by the generic engine",
	"npx nx test app":               "nx through npx: the executor's report is detected",
	"rake test":                     "rake task: go test, pytest, mocha … reports are detected; minitest output is condensed by the generic engine",
	"rake spec":                     "rake spec: RSpec's report is condensed by the generic engine (no Ruby filter yet)",
	"rake lint":                     "rake task: eslint/tsc reports are detected; rubocop output is condensed by the generic engine",
	"deno test":                     "no Deno filter yet: deno's report is condensed by the generic engine, errors kept by the guard",
	"deno check main.ts":            "no Deno filter yet: diagnostics are error lines the guard keeps",
	"deno lint":                     "no Deno filter yet: diagnostics are error lines the guard keeps",
	"deno task test":                "deno task: the report it prints is detected or condensed by the generic engine",
	"cargo nextest run":             "no nextest filter yet: PASS lines folded by the generic engine, FAIL blocks kept by the guard",
	"composer test":                 "composer script: the report it prints (pytest, vitest …) is detected; PHPUnit output is condensed by the generic engine",
	"composer run-script typecheck": "composer script: tsc's report is detected",
	"npm run testonly":              "a test script whose name no npm filter claims (npm-test takes test, test:x, test-x): vitest's report is detected",
}

var notRewritten = map[string]string{
	"cat a.go":                                 "file read: must stay byte-exact, and a rewrite would cost a permission prompt",
	"env FOO=1 cat a.go":                       "file read: must stay byte-exact, and a rewrite would cost a permission prompt",
	"cat file.txt":                             "file read: must stay byte-exact, and a rewrite would cost a permission prompt",
	"bat -p a.go":                              "file read: must stay byte-exact, and a rewrite would cost a permission prompt",
	"head -n 5 a.go":                           "file read: must stay byte-exact, and a rewrite would cost a permission prompt",
	"tail -n 5 a.go":                           "file read: must stay byte-exact, and a rewrite would cost a permission prompt",
	"node examples/search/index.js":            "node <script> prints arbitrary program output",
	"node server.js":                           "node <script> prints arbitrary program output (and may be a server)",
	"python3 script.py":                        "python <script> prints arbitrary program output",
	"python3 -c print(1)":                      "python -c prints arbitrary program output",
	"uv run python script.py":                  "python <script> prints arbitrary program output",
	"uv run script.py":                         "a script prints arbitrary program output",
	"react-scripts test":                       "react-scripts test watches by default (CI=true decides, which the hook cannot see)",
	"react-scripts test --watchAll=false":      "react-scripts test: the hook stays out of react-scripts/craco entirely (watch mode depends on CI)",
	"craco test":                               "craco test watches by default (CI=true decides, which the hook cannot see)",
	"pytest --pdb":                             "the debugger waits for input",
	"uv run pytest --pdb":                      "the debugger waits for input",
	"poetry run pytest --pdb":                  "the debugger waits for input",
	"npm run test:watch":                       "a watch script never exits",
	"npm update -i":                            "interactive",
	"make run-server":                          "a target named like a server or watcher (run, serve, dev, watch…) may never exit",
	"git commit -pm x":                         "--patch is interactive",
	"git show HEAD:README.md":                  "prints a file's exact bytes (the git-show filter passes blobs through as is)",
	"go test -json ./...":                      "the agent asked for JSON; lx renders it as text only when run as lx go test -json",
	"curl -o out.tgz https://example.com":      "-o writes the body to a file; the curl filter would only see progress",
	"curl -w %{http_code} https://example.com": "-w prints a custom format meant for scripts",
	"jq . a.json":                              "jq prints the data the agent asked to shape; compacting it would change the answer",
	"git add -A":                               "write command: prints nothing or a line per path, which the list filter keeps anyway",
	"git rm -r --cached dist":                  "write command: prints a line per path, which the list filter keeps anyway",
	"git clean -n -d":                          "prints a line per path, which the list filter keeps anyway",
	"git notes list":                           "prints a line per note, which the list filter keeps anyway",
	"cc -c x.c":                                "compiler drivers write data to stdout under many flags (-E, -M, -###, -o -, -print-*); the hook leaves direct compiler runs alone",
	"gcc -Wall x.c -o x":                       "compiler drivers write data to stdout under many flags (-E, -M, -###, -o -, -print-*); the hook leaves direct compiler runs alone",
	"command git status":                       "command is a shell builtin that lx cannot exec (Debian, Ubuntu and Alpine have no command executable); the rewriter peels an unquoted `command` and puts lx after it",
	"git stash -p show":                        "an option before the subcommand makes it git stash push --patch (show is a pathspec): interactive",
	"python -i -m pytest":                      "-i opens an interactive prompt once the module is done",
	"ninja serve":                              "a target named like a server or watcher (run, serve, dev, watch…) may never exit",
	"journalctl -F _SYSTEMD_UNIT":              "-F lists a field's values; cli.ShouldStream passes it through, so a rewrite gains nothing",
	"cmake --build build --target run":         "a target named like a server or watcher (run, serve, dev, watch…) may never exit",
	"uv run python -ui -m pytest":              "-i opens an interactive prompt once the module is done",
	"env -u X LX_RAW=1 go test ./...":          "LX_RAW / LX_OFF: the user asked for the raw command",
	"uv run env LX_OFF=1 pytest":               "LX_RAW / LX_OFF: the user asked for the raw command",
}

var coverageGaps = map[string]string{
	"go list ./...":          "go list: hook rule needs the -f/-json exclusions of the go-list filter",
	"go list -m all":         "go list: hook rule needs the -f/-json exclusions of the go-list filter",
	"pip show requests":      "pip show/uninstall: short outputs, not in the hook table",
	"pip uninstall -y flask": "pip show/uninstall: short outputs, not in the hook table",
	"yarn jest --ci":         "yarn/pnpm/bun <tool>: a package script of the same name would run instead, so pkgOK only trusts script names",
	"http GET https://x":     "HTTPie/xh: not in the hook table (curl is)",
	"wget -qO- https://x":    "wget: not in the hook table (curl is)",
}

func condenses(argv []string) (bool, string) {
	c := &engine.Context{Argv: argv}
	if engine.MachineReadableAny(c) {
		return false, "machine-readable"
	}
	f, fc := engine.Resolve(c)
	if f == nil {
		return false, "no filter"
	}
	if st, ok := f.(engine.Streamer); ok && st.Stream(fc) {
		return false, f.Name() + " streams"
	}
	return true, f.Name()
}

func TestHookFilterContract(t *testing.T) {
	if len(contractCommands) < 200 {
		t.Fatalf("only %d contract commands", len(contractCommands))
	}
	var bad []string
	seen := map[string]bool{}
	for _, cmd := range contractCommands {
		if seen[cmd] {
			bad = append(bad, fmt.Sprintf("%q: listed twice", cmd))
		}
		seen[cmd] = true
		argv := strings.Fields(cmd)
		sup := hook.Supported(argv)
		cond, why := condenses(argv)
		_, generic := genericOnly[cmd]
		_, runner := runnerOnly[cmd]
		_, deliberate := notRewritten[cmd]
		_, gap := coverageGaps[cmd]
		skip := deliberate || gap
		switch {
		case generic && runner, (generic || runner) && skip, deliberate && gap:
			bad = append(bad, fmt.Sprintf("%q: listed in more than one exception map", cmd))
		case generic || runner:
			if !sup || cond {
				bad = append(bad, fmt.Sprintf("%q: stale genericOnly/runnerOnly entry (Supported=%v, filter: %s)", cmd, sup, why))
			}
		case skip:
			if sup || !cond {
				bad = append(bad, fmt.Sprintf("%q: stale notRewritten/coverageGaps entry (Supported=%v, filter: %s)", cmd, sup, why))
			}
		case sup != cond:
			bad = append(bad, fmt.Sprintf("%q: Supported=%v but filter: %s", cmd, sup, why))
		}
	}
	for name, m := range map[string]map[string]string{"genericOnly": genericOnly, "runnerOnly": runnerOnly, "notRewritten": notRewritten, "coverageGaps": coverageGaps} {
		for cmd, reason := range m {
			if strings.TrimSpace(reason) == "" {
				bad = append(bad, fmt.Sprintf("%s[%q] has no reason", name, cmd))
			}
			if !seen[cmd] {
				bad = append(bad, fmt.Sprintf("%s[%q] is not in contractCommands", name, cmd))
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("%d hook/filter contract violations:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}

var contractWrappers = []string{"uv run", "poetry run", "pdm run", "uvx", "env -u X", "env A=1",
	"timeout 5", "gtimeout 5", "nice -n 5", "nohup", "time -p"}

func TestHookFilterContractWrapped(t *testing.T) {
	var bad []string
	for _, cmd := range contractCommands {
		_, generic := genericOnly[cmd]
		_, runner := runnerOnly[cmd]
		_, deliberate := notRewritten[cmd]
		_, gap := coverageGaps[cmd]
		if generic || runner || deliberate || gap {
			continue
		}
		for _, w := range contractWrappers {
			argv := append(strings.Fields(w), strings.Fields(cmd)...)
			sup := hook.Supported(argv)
			cond, why := condenses(argv)
			if sup != cond {
				bad = append(bad, fmt.Sprintf("%q: Supported=%v but filter: %s", strings.Join(argv, " "), sup, why))
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("%d hook/filter contract violations under wrappers:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}

func TestResolveProjectRunner(t *testing.T) {
	c := &engine.Context{Argv: []string{"poetry", "run", "jest"}, Exit: 1, Cwd: "/w"}
	f, fc := engine.Resolve(c)
	if f == nil || f.Name() != "jest" {
		t.Fatalf("Resolve(poetry run jest) = %v", f)
	}
	if !reflect.DeepEqual(fc.Argv, []string{"jest"}) || fc.Exit != 1 || fc.Cwd != "/w" {
		t.Errorf("resolved context = %+v", *fc)
	}
	if !reflect.DeepEqual(c.Argv, []string{"poetry", "run", "jest"}) {
		t.Errorf("caller's context changed: %q", c.Argv)
	}
}

func TestResolveKeepsCorpusFilters(t *testing.T) {
	dirs := []string{filepath.Join(fixture.Root(), "testdata", "corpus")}
	pkgs, _ := filepath.Glob(filepath.Join(fixture.Root(), "internal", "*", "*", "testdata"))
	pkgs2, _ := filepath.Glob(filepath.Join(fixture.Root(), "internal", "*", "testdata"))
	dirs = append(dirs, append(pkgs, pkgs2...)...)
	n := 0
	for _, dir := range dirs {
		cases, err := fixture.ReadAll(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("%s: %v", dir, err)
		}
		for _, cs := range cases {
			if len(cs.Meta.Argv) == 0 {
				continue
			}
			n++
			c := cs.Context()
			want := engine.Find(c)
			got, fc := engine.Resolve(c)
			if got != want || fc != c {
				t.Errorf("%s/%s (%q): Find = %v, Resolve = %v", dir, cs.Name, cs.Meta.Argv, want, got)
			}
		}
	}
	t.Logf("%d captures: Resolve == Find", n)
	if n < 400 {
		t.Errorf("only %d captures checked", n)
	}
}

func TestRewriteWrappers(t *testing.T) {
	const same = "\x00same"
	cases := []struct{ in, want string }{

		{`uv run pytest -x`, `lx uv run pytest -x`},
		{`uv run --with ruff pytest`, `lx uv run --with ruff pytest`},
		{`poetry run mypy src`, `lx poetry run mypy src`},
		{`uvx ruff check .`, `lx uvx ruff check .`},
		{`pipx run ruff check .`, `lx pipx run ruff check .`},
		{`FOO=1 uv run pytest`, `FOO=1 lx uv run pytest`},
		{`pdm run pytest -q`, `lx pdm run pytest -q`},
		{`uv tool run ruff check .`, `lx uv tool run ruff check .`},
		{`timeout 600 uv run pytest -x 2>&1`, `timeout 600 lx uv run pytest -x 2>&1`},
		{`env -u VIRTUAL_ENV uv run pytest`, `lx env -u VIRTUAL_ENV uv run pytest`},
		{`uv run python`, same},
		{`uv run python script.py`, same},
		{`poetry run pytest --pdb`, same},
		{`uvx ruff check --watch`, same},
		{`hatch run test:cov`, same},
		{`uv sync`, same},
		{`uv run`, same},
		{`uv run lx pytest`, same},
		{`env -i uv run pytest`, same},

		{`git grep -n foo`, `lx git grep -n foo`},
		{`git worktree list`, `lx git worktree list`},
		{`git shortlog -sn`, `lx git shortlog -sn`},
		{`git submodule status`, `lx git submodule status`},
		{`git ls-remote origin`, `lx git ls-remote origin`},
		{`git worktree list --porcelain`, same},
		{`git grep -z foo`, same},
		{`git grep -q foo`, same},
		{`git grep -O foo`, same},
		{`git grep -l foo`, same},
		{`git worktree add ../x`, same},
		{`git submodule update --init`, same},

		{`gotip test ./...`, `lx gotip test ./...`},
		{`go1.22.3 vet ./...`, `lx go1.22.3 vet ./...`},
		{`gotip run .`, same},
		{`go1.22.3 test -json ./...`, same},

		{`command go test ./...`, `command lx go test ./...`},
		{`\command go test ./...`, same},
		{`"command" go test ./...`, same},
		{`env -u X command go test ./...`, `lx env -u X command go test ./...`},

		{`env -u X LX_RAW=1 go test ./...`, same},
		{`uv run env LX_OFF=yes pytest`, same},
		{`env -u X LX_RAW=0 go test ./...`, `lx env -u X LX_RAW=0 go test ./...`},
		{`env -u X LX_RAW= go test ./...`, `lx env -u X LX_RAW= go test ./...`},

		{`git stash show -p`, `lx git stash show -p`},
		{`git stash show -p stash@{1}`, `lx git stash show -p stash@{1}`},
		{`git stash -p show`, same},
		{`git stash --patch`, same},
		{`git stash push -p`, same},

		{`gtimeout 60 go test ./...`, `lx gtimeout 60 go test ./...`},

		{`ninja -C build`, `lx ninja -C build`},
		{`ninja -C build-debug -j 8 all`, `lx ninja -C build-debug -j 8 all`},
		{`ninja serve`, same},
		{`ninja -t targets`, same},
		{`cmake --build build --target test -j 8`, `lx cmake --build build --target test -j 8`},
		{`cmake --build build --target app run`, same},
		{`cmake --build build --target=serve`, same},
		{`cmake --build build -t dev`, same},
		{`cmake --build build -- -j8 run`, same},
		{`cmake --build build -- -j8`, `lx cmake --build build -- -j8`},
	}
	for _, c := range cases {
		got, ok := hook.Rewrite(c.in)
		want, wantOK := c.want, true
		if c.want == same {
			want, wantOK = c.in, false
		}
		if got != want || ok != wantOK {
			t.Errorf("Rewrite(%q)\n got  %q ok=%v\n want %q ok=%v (reason: %s)",
				c.in, got, ok, want, wantOK, hook.Inspect(c.in).Reason)
		}
	}
}

func TestRulesSeeWrappedCommands(t *testing.T) {
	const pending = " (pending LEAD EDIT from wrapper-peel-contract? perms.go permSegment must add wrappedTexts to the lenient texts)"
	deny := hook.Rules{Deny: []string{"Bash(git push:*)"}, Allow: []string{"Bash(lx:*)"}}
	ask := hook.Rules{Ask: []string{"Bash(git push:*)"}}

	const pendingLx = " (pending LEAD EDIT from wrapper-peel-contract? claude.go evaluateWith and perms.go permSegment must check wrappedTexts of lx's inner command)"
	for _, cmd := range []string{"lx git push", "lx timeout 5 git push", "lx uv run git push --force",
		"lx env -u X git push", "lx -v nice -n 5 poetry run git push"} {
		if rw, dec := hook.EvaluateClaude(cmd, t.TempDir(), false, deny); rw != "" || dec != "deny" {
			t.Errorf("%q under deny Bash(git push:*): rewritten %q, decision %q; want deny%s", cmd, rw, dec, pendingLx)
		}
		if rw, dec := hook.EvaluateClaude(cmd, t.TempDir(), false, ask); rw != "" || dec != "ask" {
			t.Errorf("%q under ask Bash(git push:*): rewritten %q, decision %q; want ask%s", cmd, rw, dec, pendingLx)
		}
	}

	for _, cmd := range []string{
		"timeout 5 git push",
		"uv run git push --force",
		"env -u X git push origin main",
		"poetry run git push",
		"nice -n 5 uv run env -u X git push",
	} {
		if rw, dec := hook.EvaluateClaude(cmd, t.TempDir(), false, hook.Rules{}); rw == "" || dec != "" {
			t.Fatalf("%q without rules: rewritten %q, decision %q; want a rewrite with no decision", cmd, rw, dec)
		}

		if rw, dec := hook.EvaluateClaude(cmd, t.TempDir(), false, deny); rw != "" || dec != "" {
			t.Errorf("%q under deny Bash(git push:*): rewritten %q, decision %q; want no rewrite%s", cmd, rw, dec, pending)
		}
		if rw, dec := hook.EvaluateClaude(cmd, t.TempDir(), false, ask); rw == "" || dec != "ask" {
			t.Errorf("%q under ask Bash(git push:*): rewritten %q, decision %q; want a rewrite with ask%s", cmd, rw, dec, pending)
		}
	}
}

func TestProcessResolvesWrappers(t *testing.T) {
	cases := []struct {
		category, name string
		bare           []string
		wrappers       []string
	}{
		{"node", "jest-fail", []string{"jest"}, []string{"poetry run", "env FOO=1", "timeout 600", "nice -n 5"}},
		{"node", "jest-pass", []string{"jest"}, []string{"poetry run", "uv run"}},
		{"go", "go-test-fail", []string{"go", "test", "./..."}, []string{"timeout 60", "env GOFLAGS=-count=1", "uv run"}},
		{"go", "go-test-panic", []string{"go", "test", "./..."}, []string{"nice", "time -p"}},
		{"go", "go-mod-download-x", []string{"go", "mod", "download", "-x"}, []string{"nice -n 10"}},
		{"git", "git-log-60", []string{"git", "log"}, []string{"env GIT_PAGER=cat", "nohup"}},
	}
	for _, tc := range cases {
		cs := fixture.Load(t, tc.category, tc.name)
		bare := cs.Context()
		bare.Argv = tc.bare
		want := engine.Process(bare, cs.Raw, engine.Options{})
		if want.Filter == "generic" || want.Filter == "passthrough" {
			t.Fatalf("%s: bare %q not filtered (%s)", tc.name, tc.bare, want.Filter)
		}
		for _, w := range tc.wrappers {
			c := cs.Context()
			c.Argv = append(strings.Fields(w), tc.bare...)
			got := engine.Process(c, cs.Raw, engine.Options{})
			if got.Filter != want.Filter || got.Output != want.Output || got.Lossy != want.Lossy {
				t.Errorf("%s as %q: filter %s, want %s; output identical: %v "+
					"(pending LEAD EDIT from wrapper-peel-contract? engine.Process must use engine.Resolve)",
					tc.name, c.Argv, got.Filter, want.Filter, got.Output == want.Output)
			}
		}
	}

	raw := strings.Repeat(" M internal/engine/engine.go\n?? internal/engine/peel.go\n", 200)
	for _, argv := range [][]string{{"git", "status", "-s"}, {"uv", "run", "git", "status", "-s"}, {"env", "A=1", "git", "status", "-s"}} {
		if r := engine.Process(&engine.Context{Argv: argv}, raw, engine.Options{}); r.Filter != "passthrough" {
			t.Errorf("%q: filter %s, want passthrough "+
				"(pending LEAD EDIT from wrapper-peel-contract? engine.Process must check engine.MachineReadableAny)", argv, r.Filter)
		}
	}
}

func BenchmarkSupported(b *testing.B) {
	argvs := make([][]string, len(contractCommands))
	for i, cmd := range contractCommands {
		argvs[i] = strings.Fields(cmd)
	}
	b.ResetTimer()
	for range b.N {
		for _, argv := range argvs {
			hook.Supported(argv)
		}
	}
}
