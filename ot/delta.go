// Package ot holds the operations people make on a document and how two
// operations made at the same time are reconciled.
//
// Text is a flow: characters and paragraph marks in one sequence, where a mark
// is a "\n" whose attributes are those of the paragraph it ends, as Word keeps
// them in its pilcrow. Every flow ends with a mark. Splitting a paragraph is
// inserting a mark and joining two is deleting one, so that typing, pressing
// Enter and deleting across paragraphs all transform the same way.
//
// An edit is a Delta: a walk along the flow that retains, inserts and deletes.
// Offsets and lengths count UTF-16 code units, as Dart strings do. The
// algorithms are those of Quill's Delta, and the Dart package runs the same
// ones, checked against the vectors in testdata/ot.
package ot

import (
	"errors"
	"maps"
	"unicode/utf8"
)

// Attrs are formatting attributes. In a retain, an empty value removes the
// attribute.
type Attrs map[string]string

// Op is one step of a Delta: exactly one of Insert, Delete and Retain is set.
type Op struct {
	Insert string `json:"i,omitempty"`
	Delete int    `json:"d,omitempty"`
	Retain int    `json:"r,omitempty"`
	Attrs  Attrs  `json:"a,omitempty"`
}

// Delta is a change to a flow, or a whole flow when it only inserts.
type Delta []Op

// ErrSplit is returned when an offset falls between the two halves of a
// surrogate pair.
var ErrSplit = errors.New("ot: offset inside a surrogate pair")

// Len is the length of the op in UTF-16 code units.
func (o Op) Len() int {
	switch {
	case o.Delete > 0:
		return o.Delete
	case o.Retain > 0:
		return o.Retain
	}
	return utf16Len(o.Insert)
}

// BaseLen is the length of the flow the delta applies to, ignoring the
// retain implied at its end.
func (d Delta) BaseLen() int {
	n := 0
	for _, o := range d {
		n += o.Delete + o.Retain
	}
	return n
}

// Change is how much the delta grows the flow.
func (d Delta) Change() int {
	n := 0
	for _, o := range d {
		if o.Insert != "" {
			n += utf16Len(o.Insert)
		}
		n -= o.Delete
	}
	return n
}

// Push appends an op, merging it with the last one when they combine, and
// keeping inserts before deletes: equal deltas are equal op for op.
func (d Delta) Push(o Op) Delta {
	if o.Insert == "" && o.Delete <= 0 && o.Retain <= 0 {
		return d
	}
	if len(o.Attrs) == 0 || o.Delete > 0 {
		o.Attrs = nil
	}
	i := len(d)
	if i > 0 {
		last := &d[i-1]
		if o.Delete > 0 && last.Delete > 0 {
			last.Delete += o.Delete
			return d
		}
		if last.Delete > 0 && o.Insert != "" {
			i--
			if i == 0 {
				return append(Delta{o}, d...)
			}
			last = &d[i-1]
		}
		if sameAttrs(o.Attrs, last.Attrs) {
			if o.Insert != "" && last.Insert != "" {
				last.Insert += o.Insert
				return d
			}
			if o.Retain > 0 && last.Retain > 0 {
				last.Retain += o.Retain
				return d
			}
		}
	}
	if i == len(d) {
		return append(d, o)
	}
	d = append(d, Op{})
	copy(d[i+1:], d[i:])
	d[i] = o
	return d
}

// chop drops a final retain that changes nothing.
func (d Delta) chop() Delta {
	for len(d) > 0 && d[len(d)-1].Retain > 0 && d[len(d)-1].Attrs == nil {
		d = d[:len(d)-1]
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

// Compose is the delta that does a then b.
func Compose(a, b Delta) (Delta, error) {
	x, y := iterator{ops: a}, iterator{ops: b}
	var out Delta
	for x.more() || y.more() {
		if y.kind() == kindInsert {
			out = out.Push(y.next(infinite))
			continue
		}
		if x.kind() == kindDelete {
			out = out.Push(x.next(infinite))
			continue
		}
		n := min(x.peekLen(), y.peekLen())
		xo, err := x.cut(n)
		if err != nil {
			return nil, err
		}
		yo := y.next(n)
		switch {
		case yo.Retain > 0:
			o := Op{Retain: xo.Retain, Insert: xo.Insert}
			o.Attrs = composeAttrs(xo.Attrs, yo.Attrs, xo.Retain > 0)
			out = out.Push(o)
		case yo.Delete > 0 && xo.Retain > 0:
			out = out.Push(yo)
		}
	}
	return out.chop(), nil
}

// Transform rebases b, made concurrently with a, to apply after a: a then
// Transform(a, b) gives the same flow as b then Transform(b, a). When both
// insert at the same place, aFirst says whose text comes first; when both set
// the same attribute, the one ordered second wins.
func Transform(a, b Delta, aFirst bool) Delta {
	x, y := iterator{ops: a}, iterator{ops: b}
	var out Delta
	for x.more() || y.more() {
		if x.kind() == kindInsert && (aFirst || y.kind() != kindInsert) {
			out = out.Push(Op{Retain: x.next(infinite).Len()})
			continue
		}
		if y.kind() == kindInsert {
			out = out.Push(y.next(infinite))
			continue
		}
		n := min(x.peekLen(), y.peekLen())
		xo, yo := x.next(n), y.next(n)
		switch {
		case xo.Delete > 0:
		case yo.Delete > 0:
			out = out.Push(yo)
		default:
			out = out.Push(Op{Retain: n, Attrs: transformAttrs(xo.Attrs, yo.Attrs, aFirst)})
		}
	}
	return out.chop()
}

// TransformPosition moves an offset over d. At an insert made at the offset,
// dFirst puts the offset after the inserted text.
func TransformPosition(d Delta, pos int, dFirst bool) int {
	offset := 0
	for _, o := range d {
		if offset > pos {
			break
		}
		n := o.Len()
		switch {
		case o.Delete > 0:
			pos -= min(n, pos-offset)
			continue
		case o.Insert != "" && (offset < pos || dFirst):
			pos += n
		}
		offset += n
	}
	return pos
}

func sameAttrs(a, b Attrs) bool {
	return len(a) == len(b) && maps.Equal(a, b)
}

func composeAttrs(a, b Attrs, keepRemovals bool) Attrs {
	out := Attrs{}
	for k, v := range b {
		if v != "" || keepRemovals {
			out[k] = v
		}
	}
	for k, v := range a {
		if _, set := b[k]; !set {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func transformAttrs(a, b Attrs, aFirst bool) Attrs {
	if !aFirst || len(a) == 0 {
		return b
	}
	var out Attrs
	for k, v := range b {
		if _, set := a[k]; !set {
			if out == nil {
				out = Attrs{}
			}
			out[k] = v
		}
	}
	return out
}

const infinite = int(^uint(0) >> 1)

const (
	kindRetain = iota
	kindInsert
	kindDelete
)

// iterator walks a delta op by op, handing out pieces of the length asked;
// past the end it hands out an endless retain.
type iterator struct {
	ops    Delta
	i      int
	offset int // in the current op, in UTF-16 units
	byteAt int // the same offset in bytes, for inserts
}

func (it *iterator) more() bool {
	return it.i < len(it.ops)
}

func (it *iterator) kind() int {
	if it.i == len(it.ops) {
		return kindRetain
	}
	switch o := it.ops[it.i]; {
	case o.Delete > 0:
		return kindDelete
	case o.Retain > 0:
		return kindRetain
	}
	return kindInsert
}

func (it *iterator) peekLen() int {
	if it.i == len(it.ops) {
		return infinite
	}
	return it.ops[it.i].Len() - it.offset
}

// next hands out the next n units, or the rest of the current op if shorter.
// An insert is only ever handed out whole or from cut.
func (it *iterator) next(n int) Op {
	o, _ := it.cut(n)
	return o
}

func (it *iterator) cut(n int) (Op, error) {
	if it.i == len(it.ops) {
		return Op{Retain: n}, nil
	}
	o := it.ops[it.i]
	left := o.Len() - it.offset
	whole := n >= left
	if whole {
		n = left
	}
	switch {
	case o.Delete > 0:
		o.Delete = n
	case o.Retain > 0:
		o.Retain = n
	default:
		from := it.byteAt
		to := len(o.Insert)
		if !whole {
			var ok bool
			if to, ok = utf16Advance(o.Insert, from, n); !ok {
				return Op{}, ErrSplit
			}
		}
		o.Insert = o.Insert[from:to]
		it.byteAt = to
	}
	if whole {
		it.i++
		it.offset, it.byteAt = 0, 0
	} else {
		it.offset += n
	}
	return o, nil
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// utf16Advance is the byte offset n UTF-16 units after the byte offset from,
// and false if that falls inside a character's surrogate pair.
func utf16Advance(s string, from, n int) (int, bool) {
	i := from
	for n > 0 {
		if i >= len(s) {
			return i, false
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		units := 1
		if r >= 0x10000 {
			units = 2
		}
		if units > n {
			return i, false
		}
		n -= units
		i += size
	}
	return i, true
}
