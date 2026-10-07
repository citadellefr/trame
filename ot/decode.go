package ot

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// DecodeEdit reads an edit the way encoding/json does, but in one scan when
// it is written the way clients write it: known keys, each once, strings
// without surrogate escapes. Anything else is left to encoding/json, so
// that both read the same edits.
//
// exact tells whether data is as compact as the edit can be written, and
// can stand for it.
func DecodeEdit(data []byte) (e Edit, exact bool, err error) {
	r := reader{data: data}
	if e, ok := r.edit(); ok && r.end() {
		return e, !r.loose, nil
	}
	if err := json.Unmarshal(data, (*plainEdit)(&e)); err != nil {
		return nil, false, err
	}
	return e, false, nil
}

type plainEdit Edit

func (e *Edit) UnmarshalJSON(data []byte) error {
	out, _, err := DecodeEdit(data)
	if err == nil {
		*e = out
	}
	return err
}

// reader scans an edit; every method that fails leaves it to the caller to
// give up on the fast way.
type reader struct {
	data  []byte
	i     int
	loose bool // blanks, or what encoding/json would write differently
}

func (r *reader) skip() {
	for r.i < len(r.data) {
		switch r.data[r.i] {
		case ' ', '\t', '\n', '\r':
			r.loose = true
			r.i++
		default:
			return
		}
	}
}

// byte consumes b when it comes next.
func (r *reader) byte(b byte) bool {
	r.skip()
	if r.i < len(r.data) && r.data[r.i] == b {
		r.i++
		return true
	}
	return false
}

func (r *reader) end() bool {
	r.skip()
	return r.i == len(r.data)
}

// key is a key of an object, without escapes, and the colon after it.
func (r *reader) key() ([]byte, bool) {
	r.skip()
	if r.i >= len(r.data) || r.data[r.i] != '"' {
		return nil, false
	}
	start := r.i + 1
	i := start
	for i < len(r.data) && r.data[i] != '"' {
		if c := r.data[i]; c < 0x20 || c == '\\' || c >= utf8.RuneSelf {
			return nil, false
		}
		i++
	}
	if i >= len(r.data) {
		return nil, false
	}
	r.i = i + 1
	return r.data[start:i], r.byte(':')
}

func (r *reader) string() (string, bool) {
	r.skip()
	if r.i >= len(r.data) || r.data[r.i] != '"' {
		return "", false
	}
	start := r.i + 1
	i := start
	high := false
	for i < len(r.data) && r.data[i] != '"' && r.data[i] != '\\' {
		if c := r.data[i]; c < 0x20 {
			return "", false
		} else if c >= utf8.RuneSelf {
			high = true
		}
		i++
	}
	if i >= len(r.data) {
		return "", false
	}
	if r.data[i] == '"' {
		s := r.data[start:i]
		if high && !utf8.Valid(s) {
			return "", false
		}
		r.i = i + 1
		return string(s), true
	}
	buf := append([]byte(nil), r.data[start:i]...)
	for ; i < len(r.data); i++ {
		switch c := r.data[i]; {
		case c == '"':
			if high && !utf8.Valid(buf) {
				return "", false
			}
			r.i = i + 1
			return string(buf), true
		case c < 0x20:
			return "", false
		case c != '\\':
			high = high || c >= utf8.RuneSelf
			buf = append(buf, c)
		default:
			i++
			if i >= len(r.data) {
				return "", false
			}
			switch e := r.data[i]; e {
			case '"', '\\', '/':
				buf = append(buf, e)
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case 'r':
				buf = append(buf, '\r')
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case 'u':
				v, ok := hex4(r.data, i+1)
				if !ok || 0xD800 <= v && v <= 0xDFFF {
					return "", false
				}
				buf = utf8.AppendRune(buf, v)
				i += 4
			default:
				return "", false
			}
		}
	}
	return "", false
}

func hex4(data []byte, i int) (rune, bool) {
	if i+4 > len(data) {
		return 0, false
	}
	var v rune
	for _, h := range data[i : i+4] {
		switch {
		case '0' <= h && h <= '9':
			h -= '0'
		case 'a' <= h && h <= 'f':
			h -= 'a' - 10
		case 'A' <= h && h <= 'F':
			h -= 'A' - 10
		default:
			return 0, false
		}
		v = v<<4 | rune(h)
	}
	return v, true
}

// int is a non-negative integer written in digits only.
func (r *reader) int() (int, bool) {
	r.skip()
	start := r.i
	n := 0
	for r.i < len(r.data) && '0' <= r.data[r.i] && r.data[r.i] <= '9' {
		n = n*10 + int(r.data[r.i]-'0')
		r.i++
	}
	digits := r.i - start
	if digits == 0 || digits > 15 || digits > 1 && r.data[start] == '0' {
		return 0, false
	}
	if r.i < len(r.data) {
		switch r.data[r.i] {
		case '.', 'e', 'E':
			return 0, false
		}
	}
	return n, true
}

// value is the JSON value at the reader, as it is written.
func (r *reader) value() (json.RawMessage, bool) {
	r.skip()
	canonical := true
	end, ok := scanValue(r.data, r.i, 0, &canonical)
	if !ok {
		return nil, false
	}
	r.loose = r.loose || !canonical
	v := bytes.Clone(r.data[r.i:end])
	r.i = end
	return v, true
}

// object calls member with each key of the object at the reader, once.
func (r *reader) object(member func(key []byte) bool) bool {
	if !r.byte('{') {
		return false
	}
	if r.byte('}') {
		return true
	}
	for {
		key, ok := r.key()
		if !ok || !member(key) {
			return false
		}
		if !r.byte(',') {
			return r.byte('}')
		}
	}
}

func (r *reader) edit() (Edit, bool) {
	if !r.byte('[') {
		return nil, false
	}
	e := make(Edit, 0, 1)
	if r.byte(']') {
		return e, true
	}
	for {
		var c Change
		if !r.change(&c) {
			return nil, false
		}
		e = append(e, c)
		if !r.byte(',') {
			return e, r.byte(']')
		}
	}
}

func (r *reader) change(c *Change) bool {
	var seen uint16
	return r.object(func(key []byte) (ok bool) {
		var bit uint16
		switch string(key) {
		case "o":
			bit = 1 << 0
			c.Op, ok = r.opName()
		case "id":
			bit = 1 << 1
			c.ID, ok = r.string()
		case "t":
			bit = 1 << 2
			c.Type, ok = r.string()
		case "p":
			bit = 1 << 3
			c.Parent, ok = r.string()
		case "k":
			bit = 1 << 4
			c.Key, ok = r.string()
		case "a":
			bit = 1 << 5
			c.Attrs, ok = r.values()
		case "x":
			bit = 1 << 6
			c.Text, ok = r.delta()
		case "c":
			bit = 1 << 7
			c.Cells, ok = r.cells()
		case "dim":
			bit = 1 << 8
			c.Dim, ok = r.string()
		case "at":
			bit = 1 << 9
			c.At, ok = r.int()
		case "n":
			bit = 1 << 10
			c.N, ok = r.int()
		}
		if bit == 0 || seen&bit != 0 {
			return false
		}
		seen |= bit
		return ok
	})
}

// opName reads the name of an operation, which are known: none is copied.
func (r *reader) opName() (string, bool) {
	r.skip()
	if r.i+5 <= len(r.data) && r.data[r.i] == '"' && r.data[r.i+4] == '"' {
		for _, name := range [...]string{OpNew, OpDel, OpSet, OpTxt, OpCel, OpIns, OpRem} {
			if string(r.data[r.i+1:r.i+4]) == name {
				r.i += 5
				return name, true
			}
		}
	}
	return r.string()
}

func (r *reader) values() (Values, bool) {
	v := Values{}
	ok := r.object(func(key []byte) bool {
		raw, ok := r.value()
		v[string(key)] = raw
		return ok
	})
	return v, ok
}

func (r *reader) attrs() (Attrs, bool) {
	a := Attrs{}
	ok := r.object(func(key []byte) bool {
		s, ok := r.string()
		a[string(key)] = s
		return ok
	})
	return a, ok
}

func (r *reader) delta() (Delta, bool) {
	if !r.byte('[') {
		return nil, false
	}
	d := make(Delta, 0, 4)
	if r.byte(']') {
		return d, true
	}
	for {
		var o Op
		if !r.op(&o) {
			return nil, false
		}
		d = append(d, o)
		if !r.byte(',') {
			return d, r.byte(']')
		}
	}
}

func (r *reader) op(o *Op) bool {
	var seen uint8
	return r.object(func(key []byte) (ok bool) {
		var bit uint8
		switch string(key) {
		case "i":
			bit = 1 << 0
			o.Insert, ok = r.string()
		case "d":
			bit = 1 << 1
			o.Delete, ok = r.int()
		case "r":
			bit = 1 << 2
			o.Retain, ok = r.int()
		case "a":
			bit = 1 << 3
			o.Attrs, ok = r.attrs()
		}
		if bit == 0 || seen&bit != 0 {
			return false
		}
		seen |= bit
		return ok
	})
}

func (r *reader) cells() ([]Cell, bool) {
	if !r.byte('[') {
		return nil, false
	}
	cells := []Cell{}
	if r.byte(']') {
		return cells, true
	}
	for {
		raw, ok := r.value()
		var c Cell
		if !ok || c.UnmarshalJSON(raw) != nil {
			return nil, false
		}
		cells = append(cells, c)
		if !r.byte(',') {
			return cells, r.byte(']')
		}
	}
}
