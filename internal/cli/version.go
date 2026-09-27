package cli

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// devVersion is Version's value when no -ldflags -X set it.
const devVersion = "0.1.0-dev"

// readBuildInfo is debug.ReadBuildInfo; tests swap it.
var readBuildInfo = debug.ReadBuildInfo

// versionString is what `lx version` prints, e.g.
//
//	lx v0.2.0 (3f2a1c9, 2026-09-26, go1.26.5, darwin/arm64)
//
// The version comes from, in order: Version when -ldflags set it (make build,
// make dist, release archives); the module version the go command stamped
// (`go install …@v0.2.0` gives v0.2.0); else "dev". The parenthesised parts
// are the VCS revision (7 chars, "+dirty" for a modified tree) and commit
// date when the build recorded them, then the Go version and platform.
// Missing parts are left out rather than guessed.
func versionString() string {
	bi, ok := readBuildInfo()
	if !ok {
		bi = nil
	}
	v := "dev"
	switch {
	case Version != "" && Version != devVersion:
		v = Version
	case bi != nil && bi.Main.Version != "" && bi.Main.Version != "(devel)":
		v = bi.Main.Version
	}
	var parts []string
	if bi != nil {
		var rev, date string
		dirty := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				date = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" {
			if len(rev) > 7 {
				rev = rev[:7]
			}
			if dirty {
				rev += "+dirty"
			}
			parts = append(parts, rev)
		}
		if i := strings.IndexByte(date, 'T'); i >= 0 {
			date = date[:i]
		}
		if date != "" {
			parts = append(parts, date)
		}
	}
	parts = append(parts, runtime.Version(), runtime.GOOS+"/"+runtime.GOARCH)
	return "lx " + v + " (" + strings.Join(parts, ", ") + ")"
}
