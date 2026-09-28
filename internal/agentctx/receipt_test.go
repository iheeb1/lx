package agentctx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func jsonStr(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func TestReceiptsOnlyWholeLines(t *testing.T) {
	const r = "[lx: 307→29 lines (−71%) · full output: lx show 12]"
	cases := []struct {
		out  string
		want []int
	}{
		{"FAIL\n" + r, []int{12}},
		{r, []int{12}},
		{"ok\n" + r + "\nShell cwd was reset to /repo", []int{12}},
		{"ok\n[lx: 9→2 lines (−70%) · mode verify→error · full output: /opt/bin/lx show 4 · context 91% full]", []int{4}},
		{"[lx: still running after 30s · 9 lines so far · this is not the result · output so far: lx show 5 --tail 40]\nok\n" + r, []int{5, 12}},
		{"x\r\n" + r + "\r\n", []int{12}},
		{"see " + r + " above", nil},
		{"ctx_test.go:12:" + r, nil},
		{"\t\"" + r + "\",", nil},
		{`content: "FAIL\n` + r + `"}`, nil},
		{"   12→" + r, nil},
		{"[lx: 3→1 lines (−66%) · full output: lx show 7] trailing", nil},
		{"[lx: 3→1 lines (−66%)]", nil},
		{"[lx: a newer run of this command exists: lx show 9 (failed)]", nil},
	}
	for _, c := range cases {
		if got := receipts(jsonStr(c.out), false); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("receipts(%q) = %v, want %v", c.out, got, c.want)
		}
	}
	block, _ := json.Marshal([]map[string]string{{"type": "text", "text": "ok\n" + r}})
	if got := receipts(block, false); fmt.Sprint(got) != "[12]" {
		t.Errorf("text blocks: %v", got)
	}
}

func TestReceiptsNestedCodeModeOutput(t *testing.T) {
	inner, _ := json.Marshal(map[string]any{"output": "ok\n[lx: 5→1 lines (−80%) · full output: lx show 7]", "exit_code": 0})
	raw := jsonStr("Script completed\nOutput:\n" + string(inner))
	if got := receipts(raw, true); fmt.Sprint(got) != "[7]" {
		t.Errorf("nested: %v", got)
	}
	if got := receipts(raw, false); got != nil {
		t.Errorf("a two-level receipt is not a line of Bash output: %v", got)
	}
	src, _ := json.Marshal(map[string]any{"output": `want := "x\n[lx: 5→1 lines (−80%) · full output: lx show 7]"`})
	if got := receipts(jsonStr(string(src)), true); got != nil {
		t.Errorf("a receipt inside printed source is not one: %v", got)
	}
}

func TestReceiptsInLargeOutputs(t *testing.T) {
	big := strings.Repeat("line of output\n", 5000)
	if got := receipts(jsonStr(big+"[lx: 5,001→40 lines (−99%) · full output: lx show 3]"), false); fmt.Sprint(got) != "[3]" {
		t.Errorf("tail receipt: %v", got)
	}
	r := "[lx: 5,001→40 lines (−99%) · full output: lx show 3]"
	at := jsonStr(big + r + "\n" + strings.Repeat("x", receiptScan-len(r)-3))
	if got := receipts(at, false); fmt.Sprint(got) != "[3]" || !strings.HasPrefix(string(at[len(at)-receiptScan:]), "[lx: ") {
		t.Errorf("receipt at the window's first byte: %v", got)
	}
	mid := jsonStr(big + "x" + r + "\n" + strings.Repeat("x", receiptScan-len(r)-3))
	if got := receipts(mid, false); got != nil {
		t.Errorf("the window start is not a line start: %v", got)
	}
}
