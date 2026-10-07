package ot

import (
	"errors"
	"strings"
)

var (
	ErrInvalid = errors.New("ot: invalid operation")
	ErrLength  = errors.New("ot: operation longer than the document")
	ErrNoMark  = errors.New("ot: a flow must end with a paragraph mark")
)

// Check tells whether d is well formed: each op does one thing, and an
// insert sets attributes rather than removing them.
func (d Delta) Check() error {
	for _, o := range d {
		kinds := 0
		if o.Insert != "" {
			kinds++
		}
		if o.Delete != 0 {
			kinds++
		}
		if o.Retain != 0 {
			kinds++
		}
		if kinds != 1 || o.Delete < 0 || o.Retain < 0 || o.Delete > 0 && o.Attrs != nil {
			return ErrInvalid
		}
		for k, v := range o.Attrs {
			if k == "" || v == "" && o.Insert != "" {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Doc is a flow being edited. It is kept paragraph by paragraph, so that an
// edit only rebuilds the paragraphs it touches.
type Doc struct {
	paras []para
	size  int
}

type para struct {
	flow Delta // inserts ending with the paragraph's mark
	size int
}

// NewDoc makes a document of a flow, which only inserts.
func NewDoc(flow Delta) (*Doc, error) {
	paras, err := paragraphs(flow, -1)
	if err != nil {
		return nil, err
	}
	if paras == nil {
		return nil, ErrNoMark
	}
	d := &Doc{paras: paras}
	for _, p := range paras {
		d.size += p.size
	}
	return d, nil
}

// Len is the length of the flow, marks included.
func (d *Doc) Len() int {
	return d.size
}

// Paragraphs are the document's paragraphs, each ending with its mark. They
// are the document's own, not to be modified.
func (d *Doc) Paragraphs() []Delta {
	out := make([]Delta, len(d.paras))
	for i, p := range d.paras {
		out[i] = p.flow
	}
	return out
}

// Clone is a copy that later edits of d leave alone.
func (d *Doc) Clone() *Doc {
	return &Doc{paras: append([]para(nil), d.paras...), size: d.size}
}

// Delta is the whole flow as inserts.
func (d *Doc) Delta() Delta {
	return flowOf(d.paras)
}

// flowOf joins paragraphs into one flow, runs of equal attributes as one
// insert. The text is built once, whatever the number of runs: joining it
// paragraph by paragraph would copy it as many times.
func flowOf(paras []para) Delta {
	n := 0
	for _, p := range paras {
		for _, o := range p.flow {
			n += len(o.Insert)
		}
	}
	var text strings.Builder
	text.Grow(n)
	var out Delta
	var ends []int
	for _, p := range paras {
		for _, o := range p.flow {
			if len(out) == 0 || !sameAttrs(o.Attrs, out[len(out)-1].Attrs) {
				out = append(out, Op{Attrs: o.Attrs})
				ends = append(ends, 0)
			}
			text.WriteString(o.Insert)
			ends[len(ends)-1] = text.Len()
		}
	}
	all := text.String()
	start := 0
	for i, end := range ends {
		out[i].Insert = all[start:end]
		start = end
	}
	return out
}

// Apply edits the document, or leaves it as it was and returns why the delta
// does not apply to it.
func (d *Doc) Apply(delta Delta) error {
	if delta.BaseLen() > d.size {
		return ErrLength
	}
	if deletesLast(delta, d.size) {
		return ErrNoMark
	}
	delta = delta.chop()
	if len(delta) == 0 {
		return nil
	}
	start := 0
	if delta[0].Retain > 0 && delta[0].Attrs == nil {
		start = delta[0].Retain
		delta = delta[1:]
	}
	end := start + delta.BaseLen()

	// The paragraphs from the one where the edit starts to the one holding
	// the first unit it leaves alone: deleting a mark joins the next one.
	first, offset := 0, 0
	for first < len(d.paras)-1 && offset+d.paras[first].size <= start {
		offset += d.paras[first].size
		first++
	}
	last, at := first, offset
	for last < len(d.paras)-1 && at+d.paras[last].size <= end {
		at += d.paras[last].size
		last++
	}

	size := 0
	for _, p := range d.paras[first : last+1] {
		size += p.size
	}
	region := flowOf(d.paras[first : last+1])
	local := Delta{}.Push(Op{Retain: start - offset})
	for _, o := range delta {
		local = local.Push(o)
	}
	edited, err := Compose(region, local)
	if err != nil {
		return err
	}
	paras, err := paragraphs(edited, size+local.Change())
	if err != nil {
		return err
	}
	if paras == nil && first == 0 {
		return ErrNoMark
	}
	if len(paras) == last+1-first {
		copy(d.paras[first:], paras)
	} else {
		d.paras = append(d.paras[:first], append(paras, d.paras[last+1:]...)...)
	}
	for _, p := range paras {
		size -= p.size
	}
	d.size -= size
	return nil
}

// deletesLast tells whether delta deletes the last unit of a flow of size
// units, its final mark: no edit may, or an insertion made at the same time
// before it would end the flow.
func deletesLast(delta Delta, size int) bool {
	at := 0
	for _, o := range delta {
		if o.Delete > 0 && at+o.Delete == size {
			return true
		}
		at += o.Delete + o.Retain
	}
	return false
}

// paragraphs splits a flow after each mark, the last one included. When the
// size of the flow is known, the last paragraph is not measured: a keystroke
// in a long paragraph does not walk through it again.
func paragraphs(flow Delta, size int) ([]para, error) {
	var paras []para
	var p para
	measured := 0
	for k, o := range flow {
		if o.Insert == "" {
			return nil, ErrInvalid
		}
		text := o.Insert
		for text != "" {
			i := strings.IndexByte(text, '\n')
			if i < 0 {
				p.flow = p.flow.Push(Op{Insert: text, Attrs: o.Attrs})
				break
			}
			p.flow = p.flow.Push(Op{Insert: text[:i+1], Attrs: o.Attrs})
			text = text[i+1:]
			if size >= 0 && k == len(flow)-1 && text == "" {
				p.size = size - measured
			} else {
				for _, q := range p.flow {
					p.size += q.Len()
				}
				measured += p.size
			}
			paras = append(paras, p)
			p = para{}
		}
	}
	if p.flow != nil {
		return nil, ErrNoMark
	}
	return paras, nil
}
