package cli

import (
	"strings"
	"testing"
)

func TestShouldStream(t *testing.T) {
	cases := map[string]bool{
		"git status":                 false,
		"go test ./...":              false,
		"tail -f app.log":            true,
		"tail -n 50 app.log":         false,
		"docker logs -f web":         true,
		"docker logs web":            false,
		"kubectl logs -f pod":        true,
		"npm run dev":                true,
		"npm run build":              false,
		"npm test":                   false,
		"pnpm dev":                   true,
		"vite":                       true,
		"vite build":                 false,
		"vitest":                     false,
		"vitest watch":               true,
		"jest --watch":               true,
		"tsc -w":                     true,
		"tsc --noEmit":               false,
		"vim main.go":                true,
		"python3":                    true,
		"python3 script.py":          false,
		"docker compose up":          true,
		"docker compose up -d":       false,
		"docker exec -it web sh":     true,
		"kubectl port-forward svc 8": true,
		"next dev":                   true,
		"next build":                 false,
	}
	for cmd, want := range cases {
		if got := ShouldStream(strings.Fields(cmd)); got != want {
			t.Errorf("ShouldStream(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestCmdKeyNeverLeaksArgs(t *testing.T) {
	cases := map[string]string{
		"git status":                         "git status",
		"git -C /secret/path log --oneline":  "git log",
		"go test ./...":                      "go test",
		"npm run build":                      "npm run",
		"pytest tests/test_secret_thing.py":  "pytest",
		"python3 -m pytest -x":               "python3 -m pytest",
		"curl https://api.example.com?key=1": "curl",
		"grep -rn password .":                "grep",
		"docker logs web-prod-db":            "docker logs",
		"make deploy-to-secret-host":         "make deploy-to-secret-host",
	}
	for cmd, want := range cases {
		if got := cmdKey(strings.Fields(cmd)); got != want {
			t.Errorf("cmdKey(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestParseRunFlags(t *testing.T) {
	t.Setenv("LX_RAW", "")
	t.Setenv("LX_BUDGET", "")
	o, argv, err := parseRunFlags([]string{"-v", "--budget", "500", "git", "log", "-v"})
	if err != nil || !o.verbose || o.budget != 500 || strings.Join(argv, " ") != "git log -v" {
		t.Fatalf("got %+v %v %v", o, argv, err)
	}
	if _, argv, _ := parseRunFlags([]string{"--", "-weird-cmd"}); argv[0] != "-weird-cmd" {
		t.Fatalf("-- should end lx flags, got %v", argv)
	}
	if _, _, err := parseRunFlags([]string{"--nope", "ls"}); err == nil {
		t.Fatal("unknown lx flag should error")
	}
}
