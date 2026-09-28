package cli

import (
	"runtime"
	"runtime/debug"
	"strings"
)

const devVersion = "0.1.0-dev"

var readBuildInfo = debug.ReadBuildInfo

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
