package agentctx

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func FuzzParse(f *testing.F) {
	for _, p := range []string{"testdata/claude/main.jsonl", "testdata/claude/compact.jsonl", "testdata/claude/corrupt.jsonl", "testdata/codex/rollout.jsonl"} {
		b, _ := os.ReadFile(p)
		for _, line := range bytes.Split(b, []byte("\n")) {
			f.Add(line)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		parseClaude(data, &Snapshot{}, false, false)
		parseClaude(data, &Snapshot{}, true, true)
		parseCodex(data, &Snapshot{})
		(&Snapshot{Assistant: []string{string(data)}, Prompt: string(data), textAge: []int{0}}).Focus()
		inferMode(string(data))
		receipts(data, true)
		commandMatches(string(data), []string{"go", "test"})
		if !json.Valid(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
			return
		}
		var want map[string]json.RawMessage
		if json.Unmarshal(data, &want) != nil {
			return
		}
		got := map[string]bool{}
		if !fields(data, func(k, v []byte) bool {
			if !json.Valid(v) {
				t.Fatalf("value of %q is not a JSON value: %q", k, v)
			}
			got[key(k)] = true
			return true
		}) {
			t.Fatalf("fields rejected a valid object: %q", data)
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("key %q missing from %q", k, data)
			}
		}
	})
}
