// Package infra condenses container and cluster tooling output: docker
// ps/images/pull/build/logs, docker compose, kubectl get/describe/events/
// logs, and journalctl.
//
// Principles:
//   - lx never adds flags (no --tail, no --since): what the user asked for
//     is what gets condensed;
//   - followers (logs -f, journalctl -f, kubectl get -w) stream through
//     untouched (Streamer);
//   - machine formats (-o json/yaml, --format) are never matched;
//   - tables keep their rows verbatim; big tables keep the first rows plus
//     every unhealthy row, with exact counts;
//   - logs go through engine.TemplateLogs, which keeps every error record
//     (and its stack trace, folded by engine.FoldStacks) verbatim.
package infra

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() {
	engine.Register(dockerBuild{})
	engine.Register(dockerPull{})
	engine.Register(logsFilter{})
	engine.Register(dockerTable{})
	engine.Register(kubectlEvents{})
	engine.Register(kubectlGet{})
	engine.Register(kubectlDescribe{})
}

// positionals returns argv's positional arguments, skipping flags and the
// values of the flags in valueFlags ("--flag=value" needs no skipping).
// Everything after "--" is positional.
func positionals(args []string, valueFlags map[string]bool) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i+1:]...)
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if valueFlags[a] {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// dockerValue: flags taking a value, for docker and its subcommands we
// handle (global flags, build, logs, ps/images, pull, compose globals).
var dockerValue = set("--context", "-c", "-H", "--host", "--config", "-l", "--log-level",
	"--tlscacert", "--tlscert", "--tlskey",
	// build
	"-f", "--file", "-t", "--tag", "--build-arg", "--target", "--platform", "--progress", "--network",
	"--cache-from", "--cache-to", "--secret", "--ssh", "--label", "--iidfile", "--output", "-o",
	"--add-host", "--shm-size", "--ulimit", "--builder", "--build-context", "--metadata-file",
	"--attest", "--provenance", "--sbom", "--annotation", "--cgroup-parent", "--isolation", "--memory", "-m",
	// logs, ps, images
	"-n", "--tail", "--since", "--until", "--filter", "--format", "--last",
	// compose
	"-p", "--project-name", "--profile", "--env-file", "--project-directory", "--ansi", "--parallel", "--index")

// dockerCmd returns the docker subcommand path: ["build"], ["compose",
// "logs"], ["image", "ls"], ["container", "logs"], or nil when argv is not
// docker. docker-compose is reported as ["compose", …].
func dockerCmd(c *engine.Context) []string {
	switch c.Name() {
	case "docker":
		return positionals(c.Args(), dockerValue)
	case "docker-compose":
		return append([]string{"compose"}, positionals(c.Args(), dockerValue)...)
	}
	return nil
}

// dockerSub reports whether docker's command path starts with one of the
// given forms, each a space-separated path ("compose logs").
func dockerSub(c *engine.Context, forms ...string) bool {
	p := dockerCmd(c)
	for _, f := range forms {
		want := strings.Fields(f)
		if len(p) < len(want) {
			continue
		}
		ok := true
		for i, w := range want {
			if p[i] != w {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// kubectlValue: kubectl flags taking a value.
var kubectlValue = set("-n", "--namespace", "--context", "--kubeconfig", "--cluster", "--user",
	"-s", "--server", "--token", "--as", "--as-group", "--request-timeout", "-l", "--selector",
	"-o", "--output", "--field-selector", "-L", "--label-columns", "--sort-by", "--chunk-size",
	"-f", "--filename", "-k", "--kustomize", "-c", "--container", "--since", "--since-time", "--tail",
	"--limit-bytes", "--max-log-requests", "--pod-running-timeout", "--for", "--types", "--template",
	"--show-kind", "--cache-dir", "--certificate-authority", "--client-certificate", "--client-key", "-v", "--v")

// kubectlCmd returns kubectl's positionals ("get", "pods", "name") or nil.
func kubectlCmd(c *engine.Context) []string {
	switch c.Name() {
	case "kubectl", "oc":
		return positionals(c.Args(), kubectlValue)
	}
	return nil
}

// isFollow reports -f/--follow (and clusters like -tf) in args.
func isFollow(args []string, shortValue string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--follow" || strings.HasPrefix(a, "--follow=") && a != "--follow=false" {
			return true
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			for _, ch := range a[1:] {
				if ch == 'f' {
					return true
				}
				if strings.ContainsRune(shortValue, ch) {
					break
				}
			}
		}
	}
	return false
}

func baseName(p string) string { return filepath.Base(p) }

// argsAfter returns the arguments after the first one equal to word (the
// subcommand), so that flags of other levels ("docker compose -f x.yml
// logs") are not read as the subcommand's.
func argsAfter(args []string, word string) []string {
	for i, a := range args {
		if a == word {
			return args[i+1:]
		}
	}
	return nil
}
