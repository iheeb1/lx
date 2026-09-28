package agentctx

import (
	"bytes"
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

func skipSpace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func strEnd(b []byte, i int) int {
	j := i + 1
	for j <= len(b) {
		k := bytes.IndexByte(b[j:], '"')
		if k < 0 {
			return -1
		}
		j += k
		n := 0
		for p := j - 1; p > i && b[p] == '\\'; p-- {
			n++
		}
		if n%2 == 0 {
			return j + 1
		}
		j++
	}
	return -1
}

func valueEnd(b []byte, i int) int {
	if i >= len(b) {
		return -1
	}
	switch b[i] {
	case '"':
		return strEnd(b, i)
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				e := strEnd(b, i)
				if e < 0 {
					return -1
				}
				i = e
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return -1
	}
	j := i
	for j < len(b) {
		switch b[j] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			if j == i {
				return -1
			}
			return j
		}
		j++
	}
	return j
}

func fields(b []byte, fn func(k, v []byte) bool) bool { return fieldsUntil(b, nil, fn) }

func fieldsUntil(b []byte, stop func(k []byte) bool, fn func(k, v []byte) bool) bool {
	i := skipSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		return false
	}
	i = skipSpace(b, i+1)
	if i < len(b) && b[i] == '}' {
		return true
	}
	for i < len(b) && b[i] == '"' {
		ke := strEnd(b, i)
		if ke < 0 {
			return false
		}
		key := b[i+1 : ke-1]
		if stop != nil && stop(key) {
			return true
		}
		i = skipSpace(b, ke)
		if i >= len(b) || b[i] != ':' {
			return false
		}
		i = skipSpace(b, i+1)
		ve := valueEnd(b, i)
		if ve < 0 {
			return false
		}
		if !fn(key, b[i:ve]) {
			return true
		}
		i = skipSpace(b, ve)
		if i >= len(b) {
			return false
		}
		if b[i] == '}' {
			return true
		}
		if b[i] != ',' {
			return false
		}
		i = skipSpace(b, i+1)
	}
	return false
}

func elems(b []byte, fn func(v []byte)) bool {
	i := skipSpace(b, 0)
	if i >= len(b) || b[i] != '[' {
		return false
	}
	i = skipSpace(b, i+1)
	if i < len(b) && b[i] == ']' {
		return true
	}
	for i < len(b) {
		ve := valueEnd(b, i)
		if ve < 0 {
			return false
		}
		fn(b[i:ve])
		i = skipSpace(b, ve)
		if i >= len(b) {
			return false
		}
		if b[i] == ']' {
			return true
		}
		if b[i] != ',' {
			return false
		}
		i = skipSpace(b, i+1)
	}
	return false
}

func str(v []byte) string {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return ""
	}
	in := v[1 : len(v)-1]
	if bytes.IndexByte(in, '\\') < 0 && utf8.Valid(in) {
		return string(in)
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

func key(k []byte) string {
	if bytes.IndexByte(k, '\\') < 0 && utf8.Valid(k) {
		return string(k)
	}
	return str(append(append([]byte{'"'}, k...), '"'))
}

func num(v []byte) int {
	n, err := strconv.Atoi(string(v))
	if err != nil {
		if f, err := strconv.ParseFloat(string(v), 64); err == nil && f > 0 && f < 1e15 {
			return int(f)
		}
		return 0
	}
	return n
}

func field(b []byte, name string) []byte {
	var out []byte
	fields(b, func(k, v []byte) bool {
		if string(k) == name {
			out = v
			return false
		}
		return true
	})
	return out
}

func strField(b []byte, name string) string { return str(field(b, name)) }

func eachLineReverse(buf []byte, fn func(line []byte) bool) {
	var ends []int
	for i := 0; i < len(buf); {
		j := bytes.IndexByte(buf[i:], '\n')
		if j < 0 {
			ends = append(ends, len(buf))
			break
		}
		ends = append(ends, i+j)
		i += j + 1
	}
	for k := len(ends) - 1; k >= 0; k-- {
		start := 0
		if k > 0 {
			start = ends[k-1] + 1
		}
		line := bytes.TrimRight(buf[start:ends[k]], "\r")
		if len(line) > 0 && !fn(line) {
			return
		}
	}
}
