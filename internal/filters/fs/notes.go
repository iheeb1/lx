package fs

import (
	"fmt"
	"sort"
	"strings"
)

// maxNotes bounds how many diagnostic lines are printed verbatim.
const maxNotes = 40

// CapNotes returns diagnostics ("find: ./x: Permission denied") verbatim
// when there are at most maxNotes of them. Beyond that the first
// maxNotes-10 are kept and the rest are counted by message, so a flood of
// permission errors from `find /` stays exact without drowning the listing:
//
//	… +9,950 more: Permission denied ×9,948, No such file or directory ×2
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
