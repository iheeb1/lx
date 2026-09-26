package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// A minimal order-preserving JSON object editor. Settings files belong to
// the user: lx must change exactly the value it owns and write every other
// key, value and ordering back as it found it. Values are kept as the raw
// bytes they were decoded from, so numbers, escapes and unknown structures
// survive untouched (only whitespace is normalized on re-encoding).

type member struct {
	key string
	val json.RawMessage
}

type object struct{ members []member }

var errNotObject = errors.New("not a JSON object")

func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errNotObject
	}
	o := &object{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected token %v", kt)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.members = append(o.members, member{key, raw})
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return o, nil
}

func (o *object) get(key string) (json.RawMessage, bool) {
	for _, m := range o.members {
		if m.key == key {
			return m.val, true
		}
	}
	return nil, false
}

// set replaces the first member named key in place, or appends it.
func (o *object) set(key string, val json.RawMessage) {
	for i := range o.members {
		if o.members[i].key == key {
			o.members[i].val = val
			return
		}
	}
	o.members = append(o.members, member{key, val})
}

func (o *object) del(key string) {
	out := o.members[:0]
	for _, m := range o.members {
		if m.key != key {
			out = append(out, m)
		}
	}
	o.members = out
}

func (o *object) getString(key string) (string, bool) {
	raw, ok := o.get(key)
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// compact encodes the object on one line, members in order.
func (o *object) compact() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o.members {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsonString(m.key))
		b.WriteByte(':')
		if err := json.Compact(&b, m.val); err != nil {
			b.Write(m.val)
		}
	}
	b.WriteByte('}')
	return b.Bytes()
}

// indented encodes the object with 2-space indentation and a final newline.
func (o *object) indented() []byte {
	var b bytes.Buffer
	if err := json.Indent(&b, o.compact(), "", "  "); err != nil {
		return append(o.compact(), '\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// jsonString encodes s without HTML escaping (&& stays &&).
func jsonString(s string) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func parseArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	return arr, nil
}

func encodeArray(arr []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range arr {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(v)
	}
	b.WriteByte(']')
	return b.Bytes()
}
