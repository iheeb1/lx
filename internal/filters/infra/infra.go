// Package infra handles docker, kubectl and journalctl.
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

var dockerValue = set("--context", "-c", "-H", "--host", "--config", "-l", "--log-level",
	"--tlscacert", "--tlscert", "--tlskey",

	"-f", "--file", "-t", "--tag", "--build-arg", "--target", "--platform", "--progress", "--network",
	"--cache-from", "--cache-to", "--secret", "--ssh", "--label", "--iidfile", "--output", "-o",
	"--add-host", "--shm-size", "--ulimit", "--builder", "--build-context", "--metadata-file",
	"--attest", "--provenance", "--sbom", "--annotation", "--cgroup-parent", "--isolation", "--memory", "-m",

	"-n", "--tail", "--since", "--until", "--filter", "--format", "--last",

	"-p", "--project-name", "--profile", "--env-file", "--project-directory", "--ansi", "--parallel", "--index")

func dockerCmd(c *engine.Context) []string {
	switch c.Name() {
	case "docker":
		return positionals(c.Args(), dockerValue)
	case "docker-compose":
		return append([]string{"compose"}, positionals(c.Args(), dockerValue)...)
	}
	return nil
}

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

var kubectlValue = set("-n", "--namespace", "--context", "--kubeconfig", "--cluster", "--user",
	"-s", "--server", "--token", "--as", "--as-group", "--request-timeout", "-l", "--selector",
	"-o", "--output", "--field-selector", "-L", "--label-columns", "--sort-by", "--chunk-size",
	"-f", "--filename", "-k", "--kustomize", "-c", "--container", "--since", "--since-time", "--tail",
	"--limit-bytes", "--max-log-requests", "--pod-running-timeout", "--for", "--types", "--template",
	"--show-kind", "--cache-dir", "--certificate-authority", "--client-certificate", "--client-key", "-v", "--v")

func kubectlCmd(c *engine.Context) []string {
	switch c.Name() {
	case "kubectl", "oc":
		return positionals(c.Args(), kubectlValue)
	}
	return nil
}

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

func argsAfter(args []string, word string) []string {
	for i, a := range args {
		if a == word {
			return args[i+1:]
		}
	}
	return nil
}
