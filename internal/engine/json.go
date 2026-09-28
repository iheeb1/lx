package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/tokens"
)

type jv struct {
	kind byte
	keys []string
	vals []*jv
	s    string
}

const (
	maxJSONDepth   = 256
	jsonCellRunes  = 80
	jsonInlineCell = 60
	jsonExpandMin  = 100
	jsonMaxItems   = 30
	jsonKeepItems  = 20
)

var errJSONDepth = errors.New("json too deep")

func parseJSONStream(s string) ([]*jv, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var out []*jv
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		v, err := buildJV(dec, tok, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func buildJV(dec *json.Decoder, tok json.Token, depth int) (*jv, error) {
	if depth > maxJSONDepth {
		return nil, errJSONDepth
	}
	switch t := tok.(type) {
	case json.Delim:
		v := &jv{kind: 'o'}
		if t == '[' {
			v.kind = 'a'
		} else if t != '{' {
			return nil, fmt.Errorf("unexpected %v", t)
		}
		for dec.More() {
			if v.kind == 'o' {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("bad key")
				}
				v.keys = append(v.keys, k)
			}
			et, err := dec.Token()
			if err != nil {
				return nil, err
			}
			e, err := buildJV(dec, et, depth+1)
			if err != nil {
				return nil, err
			}
			v.vals = append(v.vals, e)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return v, nil
	case string:
		return &jv{kind: 's', s: t}, nil
	case json.Number:
		return &jv{kind: 'n', s: t.String()}, nil
	case bool:
		if t {
			return &jv{kind: 'b', s: "true"}, nil
		}
		return &jv{kind: 'b', s: "false"}, nil
	case nil:
		return &jv{kind: 'z', s: "null"}, nil
	}
	return nil, fmt.Errorf("unexpected token %T", tok)
}

func (v *jv) get(key string) *jv {
	for i, k := range v.keys {
		if k == key {
			return v.vals[i]
		}
	}
	return nil
}

func (v *jv) scalar() bool { return v.kind != 'o' && v.kind != 'a' }

var jsonErrorKeys = map[string]bool{"error": true, "errors": true, "message": true, "detail": true, "status": true, "code": true}

var jsonIdentKeys = []string{"name", "login", "title", "key", "id"}

type jsonRenderer struct {
	dropURLs    bool
	droppedURLs int
}

const minURLFields = 10

func countURLFields(v *jv) int {
	n := 0
	for i, e := range v.vals {
		if v.kind == 'o' && isURLField(v.keys[i], e) {
			n++
			continue
		}
		n += countURLFields(e)
	}
	return n
}

func (r *jsonRenderer) dropField(key string, v *jv) bool {
	if r == nil || !r.dropURLs || !isURLField(key, v) {
		return false
	}
	r.droppedURLs++
	return true
}

func isURLField(key string, v *jv) bool {
	if key == "_links" {
		return true
	}
	if key != "url" && key != "href" && !strings.HasSuffix(key, "_url") {
		return false
	}
	return v.kind == 's' && (strings.HasPrefix(v.s, "http://") || strings.HasPrefix(v.s, "https://"))
}

func CompactJSON(s string, budget int) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return s, false
	}
	inTokens := tokens.Count(t)
	if inTokens <= budget {
		return s, false
	}
	vals, err := parseJSONStream(t)
	if err != nil || len(vals) == 0 {
		return s, false
	}
	root := vals[0]
	if len(vals) > 1 {
		root = &jv{kind: 'a', vals: vals}
	}
	r := &jsonRenderer{dropURLs: countURLFields(root) >= minURLFields}
	var out []string
	if root.kind == 'o' {
		var hoisted []string
		hoisted, root = hoistJSONErrors(root)
		out = append(out, hoisted...)
	}
	out = append(out, r.block(root, 0, "")...)
	if r.droppedURLs > 0 {
		out = append(out, fmt.Sprintf("[dropped %d *_url fields]", r.droppedURLs))
	}
	res := strings.Join(out, "\n")
	if tokens.Count(res) >= inTokens {
		return s, false
	}
	return res, true
}

func hoistJSONErrors(root *jv) ([]string, *jv) {
	var hoisted []string
	rest := &jv{kind: 'o'}
	for i, k := range root.keys {
		v := root.vals[i]
		if jsonErrorKeys[k] {
			hoisted = append(hoisted, k+": "+verbatimJSON(v))
			continue
		}
		if v.kind == 'o' {
			sub := &jv{kind: 'o'}
			for j, k2 := range v.keys {
				if k2 == "error" || k2 == "errors" {
					hoisted = append(hoisted, k+"."+k2+": "+verbatimJSON(v.vals[j]))
					continue
				}
				sub.keys = append(sub.keys, k2)
				sub.vals = append(sub.vals, v.vals[j])
			}
			v = sub
		}
		rest.keys = append(rest.keys, k)
		rest.vals = append(rest.vals, v)
	}
	return hoisted, rest
}

func verbatimJSON(v *jv) string {
	var b strings.Builder
	writeJSON(&b, v, nil)
	return b.String()
}

func (r *jsonRenderer) inline(v *jv) string {
	var b strings.Builder
	writeJSON(&b, v, r)
	return b.String()
}

func writeJSON(b *strings.Builder, v *jv, r *jsonRenderer) {
	switch v.kind {
	case 'o':
		b.WriteByte('{')
		n := 0
		for i, k := range v.keys {
			if r.dropField(k, v.vals[i]) {
				continue
			}
			if n > 0 {
				b.WriteByte(',')
			}
			n++
			b.WriteString(quoteJSON(k))
			b.WriteByte(':')
			writeJSON(b, v.vals[i], r)
		}
		b.WriteByte('}')
	case 'a':
		b.WriteByte('[')
		items := v.vals
		cut := 0
		if r != nil && len(items) > jsonMaxItems {
			cut = len(items) - jsonKeepItems
			items = items[:jsonKeepItems]
		}
		for i, e := range items {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, e, r)
		}
		if cut > 0 {
			fmt.Fprintf(b, ",… +%d more items", cut)
		}
		b.WriteByte(']')
	case 's':
		b.WriteString(quoteJSON(v.s))
	default:
		b.WriteString(v.s)
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (r *jsonRenderer) block(v *jv, depth int, indent string) []string {
	switch v.kind {
	case 'o':
		inl := r.probe(v)
		if depth >= 2 || len(v.keys) == 0 || utf8.RuneCountInString(inl) <= jsonExpandMin {
			return []string{indent + r.inline(v)}
		}
		out := []string{indent + "{"}
		var entries [][]string
		for i, k := range v.keys {
			val := v.vals[i]
			if r.dropField(k, val) {
				continue
			}
			sub := r.block(val, depth+1, indent+"  ")
			sub[0] = indent + "  " + quoteJSON(k) + ": " + strings.TrimLeft(sub[0], " ")
			entries = append(entries, sub)
		}
		for i, e := range entries {
			if i < len(entries)-1 && !isTableBlock(e) {
				e[len(e)-1] += ","
			}
			out = append(out, e...)
		}
		return append(out, indent+"}")
	case 'a':
		if table, ok := r.table(v, indent); ok {
			return table
		}
		inl := r.probe(v)
		if depth >= 2 || len(v.vals) == 0 || utf8.RuneCountInString(inl) <= jsonExpandMin || allScalars(v.vals) && len(v.vals) <= jsonMaxItems {
			return []string{indent + r.inline(v)}
		}
		out := []string{indent + "["}
		items := v.vals
		cut := 0
		if len(items) > jsonMaxItems {
			cut = len(items) - jsonKeepItems
			items = items[:jsonKeepItems]
		}
		for i, e := range items {
			ln := indent + "  " + r.inline(e)
			if i < len(items)-1 || cut > 0 {
				ln += ","
			}
			out = append(out, ln)
		}
		if cut > 0 {
			out = append(out, fmt.Sprintf("%s  … +%d more items", indent, cut))
		}
		return append(out, indent+"]")
	}
	return []string{indent + r.inline(v)}
}

func (r *jsonRenderer) probe(v *jv) string {
	saved := r.droppedURLs
	s := r.inline(v)
	r.droppedURLs = saved
	return s
}

func isTableBlock(lines []string) bool {
	return len(lines) > 1 && strings.HasPrefix(strings.TrimLeft(lines[0], " "), `"`) &&
		strings.Contains(lines[0], "[table: ")
}

func allScalars(vs []*jv) bool {
	for _, v := range vs {
		if !v.scalar() {
			return false
		}
	}
	return true
}

func homogeneous(arr *jv) ([]string, bool) {
	if len(arr.vals) < 3 {
		return nil, false
	}
	freq := map[string]int{}
	var order []string
	for _, e := range arr.vals {
		if e.kind != 'o' || len(e.keys) == 0 {
			return nil, false
		}
		for _, k := range e.keys {
			if freq[k] == 0 {
				order = append(order, k)
			}
			freq[k]++
		}
	}
	var common []string
	for _, k := range order {
		if freq[k]*2 >= len(arr.vals) {
			common = append(common, k)
		}
	}
	if len(common) == 0 {
		return nil, false
	}
	for _, e := range arr.vals {
		have := 0
		for _, k := range common {
			if e.get(k) != nil {
				have++
			}
		}
		if have*10 < len(common)*8 {
			return nil, false
		}
	}
	scalar := 0
	first := arr.vals[0]
	for _, k := range common {
		if v := first.get(k); v != nil && v.scalar() {
			scalar++
		}
	}
	if scalar*2 < len(common) {
		return nil, false
	}
	return order, true
}

func (r *jsonRenderer) table(arr *jv, indent string) ([]string, bool) {
	cols, ok := homogeneous(arr)
	if !ok {
		return nil, false
	}
	var keep, same []string
	for _, k := range cols {
		urlCol, n := true, 0
		for _, e := range arr.vals {
			if v := e.get(k); v != nil {
				n++
				if !isURLField(k, v) && v.kind != 'z' {
					urlCol = false
				}
			}
		}
		if r.dropURLs && urlCol && (k == "_links" || k == "url" || k == "href" || strings.HasSuffix(k, "_url")) {
			for _, e := range arr.vals {
				if v := e.get(k); v != nil && v.kind != 'z' {
					r.droppedURLs++
				}
			}
			continue
		}

		if n == len(arr.vals) {
			v0 := verbatimJSON(arr.vals[0].get(k))
			constant := utf8.RuneCountInString(v0) <= jsonInlineCell
			for _, e := range arr.vals[1:] {
				if !constant {
					break
				}
				constant = verbatimJSON(e.get(k)) == v0
			}
			if constant {
				same = append(same, k+"="+r.cell(arr.vals[0].get(k)))
				continue
			}
		}
		keep = append(keep, k)
	}
	out := []string{fmt.Sprintf("%s[table: %d items]", indent, len(arr.vals))}
	if len(keep) > 0 {
		out = append(out, indent+"keys: "+strings.Join(keep, ", "))
	}
	if len(same) > 0 {
		out = append(out, indent+"all: "+strings.Join(same, ", "))
	}
	items := arr.vals
	cut := 0
	if len(items) > jsonMaxItems {
		cut = len(items) - jsonKeepItems
		items = items[:jsonKeepItems]
	}
	if len(keep) > 0 {
		for _, e := range items {
			cells := make([]string, len(keep))
			for i, k := range keep {
				if v := e.get(k); v != nil {
					cells[i] = r.cell(v)
				}
			}
			out = append(out, indent+strings.Join(cells, " | "))
		}
	}
	if cut > 0 {
		out = append(out, fmt.Sprintf("%s… +%d more items", indent, cut))
	}
	return out, true
}

var cellEscaper = strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

func (r *jsonRenderer) cell(v *jv) string {
	switch v.kind {
	case 's':
		return cutRunes(cellEscaper.Replace(v.s), jsonCellRunes)
	case 'o':
		if len(v.keys) == 0 {
			return "{}"
		}
		if inl := r.probe(v); utf8.RuneCountInString(inl) <= jsonInlineCell {
			return r.inline(v)
		}
		label := fmt.Sprintf("{…%d keys}", len(v.keys))
		if id := jsonIdent(v); id != "" {
			label = fmt.Sprintf("{…%d keys, %s}", len(v.keys), id)
		}
		return label
	case 'a':
		if len(v.vals) == 0 {
			return "[]"
		}
		if allScalars(v.vals) {
			if inl := r.probe(v); utf8.RuneCountInString(inl) <= jsonInlineCell {
				return r.inline(v)
			}
		} else {
			names := make([]string, 0, len(v.vals))
			for _, e := range v.vals {
				id := ""
				if e.kind == 'o' {
					id = jsonIdentValue(e)
				}
				if id == "" {
					names = nil
					break
				}
				names = append(names, id)
			}
			if names != nil {
				if s := "[" + strings.Join(names, ", ") + "]"; utf8.RuneCountInString(s) <= jsonInlineCell {
					return s
				}
			}
		}
		return fmt.Sprintf("[%d items]", len(v.vals))
	}
	return v.s
}

func jsonIdent(v *jv) string {
	for _, k := range jsonIdentKeys {
		if e := v.get(k); e != nil && e.scalar() && e.kind != 'z' {
			return k + "=" + cutRunes(e.s, 40)
		}
	}
	return ""
}

func jsonIdentValue(v *jv) string {
	for _, k := range jsonIdentKeys {
		if e := v.get(k); e != nil && e.scalar() && e.kind != 'z' {
			return cutRunes(e.s, 40)
		}
	}
	return ""
}

func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}
