package jstools

import "github.com/iheeb1/lx/internal/filters/focus"

func focusFirst[T any](fx *focus.Set, items []T, path func(T) string) []T {
	if !fx.HasFiles() {
		return items
	}
	var in, rest []T
	for _, it := range items {
		if fx.File(path(it)) {
			in = append(in, it)
		} else {
			rest = append(rest, it)
		}
	}
	if len(in) == 0 || len(rest) == 0 {
		return items
	}
	return append(in, rest...)
}
