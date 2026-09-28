package fs

import (
	"fmt"
	"sort"
	"strings"
)

const maxNotes = 40

func CapNotes(notes []string) []string {
	if len(notes) <= maxNotes {
		return notes
	}
	keep := maxNotes - 10
	out := append([]string(nil), notes[:keep]...)
	count := map[string]int{}
	for _, n := range notes[keep:] {
		msg := n
		if i := strings.LastIndex(n, ": "); i >= 0 {
			msg = n[i+2:]
		}
		count[msg]++
	}
	msgs := make([]string, 0, len(count))
	for m := range count {
		msgs = append(msgs, m)
	}
	sort.Slice(msgs, func(i, j int) bool {
		if count[msgs[i]] != count[msgs[j]] {
			return count[msgs[i]] > count[msgs[j]]
		}
		return msgs[i] < msgs[j]
	})
	var parts []string
	for i, m := range msgs {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("… (%d more kinds)", len(msgs)-5))
			break
		}
		parts = append(parts, fmt.Sprintf("%s ×%s", m, commaInt(count[m])))
	}
	return append(out, fmt.Sprintf("… +%s more: %s", commaInt(len(notes)-keep), strings.Join(parts, ", ")))
}
