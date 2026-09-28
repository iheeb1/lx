package git

import "github.com/iheeb1/lx/internal/filters/focus"

func focusDiff(parts []part, fx *focus.Set) ([]part, func(*fileDiff) bool) {
	if !fx.HasFiles() {
		return parts, nil
	}
	focused := map[*fileDiff]bool{}
	for _, p := range parts {
		if p.file != nil && fx.File(p.file.name) {
			focused[p.file] = true
		}
	}
	if len(focused) == 0 {
		return parts, nil
	}
	out := make([]part, 0, len(parts))
	for i := 0; i < len(parts); {
		if parts[i].file == nil {
			out = append(out, parts[i])
			i++
			continue
		}
		j := i
		for j < len(parts) && parts[j].file != nil {
			j++
		}
		for _, p := range parts[i:j] {
			if focused[p.file] {
				out = append(out, p)
			}
		}
		for _, p := range parts[i:j] {
			if !focused[p.file] {
				out = append(out, p)
			}
		}
		i = j
	}
	return out, func(f *fileDiff) bool { return focused[f] }
}
