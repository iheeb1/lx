package data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

const recordsKey = "(key)"

func compactJSON(s string, budget int) (string, bool) {
	j, ok := engine.CompactJSON(s, budget)
	if ok && countTokens(j) <= fileBudget {
		return j, true
	}
	arr, n, isMap := recordsAsArray(s)
	if !isMap {
		return j, ok
	}
	r, ok2 := engine.CompactJSON(arr, budget)
	if !ok2 || ok && countTokens(r) >= countTokens(j) {
		return j, ok
	}
	return fmt.Sprintf("[the root object's %d entries are listed as items, each with its key in a %q field]\n%s", n, recordsKey, r), true
}

func recordsAsArray(s string) (string, int, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", 0, false
	}
	var b bytes.Buffer
	b.WriteByte('[')
	n := 0
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return "", 0, false
		}
		k, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return "", 0, false
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) < 2 || raw[0] != '{' {
			return "", 0, false
		}
		if n > 0 {
			b.WriteByte(',')
		}
		kq, _ := json.Marshal(k)
		b.WriteString(`{"` + recordsKey + `":`)
		b.Write(kq)
		if inner := bytes.TrimSpace(raw[1 : len(raw)-1]); len(inner) > 0 {
			b.WriteByte(',')
			b.Write(inner)
		}
		b.WriteByte('}')
		n++
	}
	if _, err := dec.Token(); err != nil {
		return "", 0, false
	}
	if _, err := dec.Token(); err == nil {
		return "", 0, false
	}
	b.WriteByte(']')
	return b.String(), n, n >= 3
}
