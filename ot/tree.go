package ot

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"slices"
)

// A Tree is a document made of nodes: slides and the shapes on them, the
// sheets of a workbook. A node has a type, attributes and possibly a flow of
// text or a grid of cells, and sits under its parent, ordered among its siblings by key then
// id. Nodes never change parent: moving one among its siblings changes its
// key, and nodes inserted at the same place by two people both stay.
//
// A Tree changes by Edits, lists of changes that apply together. A change to
// a node that no longer exists does nothing: deleting a node wins over
// everything done to it meanwhile, which keeps Transform free of state.
type Tree struct {
	nodes map[string]*Node
	kids  map[string][]string
	size  int
}

// Node is read-only but for its text: the tree replaces nodes rather than
// changing them.
type Node struct {
	ID     string
	Type   string
	Parent string
	Key    string
	Attrs  Values
	Text   *Doc
	Grid   *Grid
}

// Values are attributes, each a JSON value.
type Values map[string]json.RawMessage

// Change is one step of an Edit:
//
//	{"o":"new","id":"…","t":"shape","p":"parent","k":"key","a":{…},"x":[flow]}
//	{"o":"del","id":"…"}                  the node and everything under it
//	{"o":"set","id":"…","k":"…","a":{…}}  a null value removes the attribute
//	{"o":"txt","id":"…","x":[delta]}
//	{"o":"cel","id":"…","c":[[row,col,{fields}],…]}  a null value removes the field
//	{"o":"ins","id":"…","dim":"r","at":5,"n":2}    rows or columns (dim "c")
//	{"o":"rem","id":"…","dim":"r","at":5,"n":2}
//
// A node created with cells, possibly none ("c":[]), holds a grid.
type Change struct {
	Op     string `json:"o"`
	ID     string `json:"id"`
	Type   string `json:"t,omitempty"`
	Parent string `json:"p,omitempty"`
	Key    string `json:"k,omitempty"`
	Attrs  Values `json:"a,omitempty"`
	Text   Delta  `json:"x,omitempty"`
	Cells  []Cell `json:"c,omitzero"`
	Dim    string `json:"dim,omitempty"`
	At     int    `json:"at,omitempty"`
	N      int    `json:"n,omitempty"`
}

const (
	OpNew = "new"
	OpDel = "del"
	OpSet = "set"
	OpTxt = "txt"
)

func grid(op string) bool {
	return op == OpCel || op == OpIns || op == OpRem
}

// Edit is what one person does at once, applied whole or not at all.
type Edit []Change

var ErrExists = errors.New("ot: node already exists")

const maxName = 64

var null = []byte("null")

// Check tells whether e is well formed.
func (e Edit) Check() error {
	for _, c := range e {
		if !name(c.ID) || c.Key != "" && !name(c.Key) {
			return ErrInvalid
		}
		for k, v := range c.Attrs {
			if k == "" || len(v) == 0 || c.Op == OpNew && bytes.Equal(v, null) {
				return ErrInvalid
			}
		}
		bare := c.Type == "" && c.Parent == "" && c.Key == "" && c.Attrs == nil && c.Text == nil
		noGrid := c.Cells == nil && c.Dim == "" && c.At == 0 && c.N == 0
		var ok bool
		switch c.Op {
		case OpNew:
			ok = name(c.Type) && c.Key != "" && (c.Parent == "" || name(c.Parent)) && (c.Text == nil || flow(c.Text)) &&
				c.Dim == "" && c.At == 0 && c.N == 0 && (c.Cells == nil || c.Text == nil && checkCells(c.Cells, false))
		case OpDel:
			ok = bare && noGrid
		case OpSet:
			ok = c.Type == "" && c.Parent == "" && c.Text == nil && (c.Key != "" || len(c.Attrs) > 0) && noGrid
		case OpTxt:
			ok = c.Type == "" && c.Parent == "" && c.Key == "" && c.Attrs == nil && c.Text.Check() == nil && noGrid
		case OpCel:
			ok = bare && len(c.Cells) > 0 && checkCells(c.Cells, true) && c.Dim == "" && c.At == 0 && c.N == 0
		case OpIns, OpRem:
			limit := MaxRows
			if c.Dim == DimCols {
				limit = MaxCols
			}
			ok = bare && c.Cells == nil && (c.Dim == DimRows || c.Dim == DimCols) &&
				1 <= c.At && c.At <= limit && 1 <= c.N && c.N <= limit
		}
		if !ok {
			return ErrInvalid
		}
	}
	return nil
}

// name is an id, a type or a key: short, printable ASCII, so that Go and
// Dart order them alike.
func name(s string) bool {
	if s == "" || len(s) > maxName {
		return false
	}
	for i := range len(s) {
		if s[i] < '!' || s[i] > '~' {
			return false
		}
	}
	return true
}

func flow(d Delta) bool {
	if d.Check() != nil || len(d) == 0 {
		return false
	}
	for _, o := range d {
		if o.Insert == "" {
			return false
		}
	}
	last := d[len(d)-1].Insert
	return last[len(last)-1] == '\n'
}

// NewTree builds a tree from the nodes an Edit creates, parents first.
func NewTree(nodes Edit) (*Tree, error) {
	t := &Tree{nodes: map[string]*Node{}, kids: map[string][]string{}}
	if err := nodes.Check(); err != nil {
		return nil, err
	}
	for _, c := range nodes {
		if c.Op != OpNew {
			return nil, ErrInvalid
		}
	}
	if err := t.Apply(nodes); err != nil {
		return nil, err
	}
	return t, nil
}

// Len is the size of the tree: its nodes and the length of their text.
func (t *Tree) Len() int {
	return t.size
}

func (t *Tree) Node(id string) *Node {
	return t.nodes[id]
}

// Children are the nodes under parent, in order; "" is the root.
func (t *Tree) Children(parent string) []*Node {
	out := make([]*Node, len(t.kids[parent]))
	for i, id := range t.kids[parent] {
		out[i] = t.nodes[id]
	}
	slices.SortFunc(out, func(a, b *Node) int {
		return cmp.Or(cmp.Compare(a.Key, b.Key), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// Edit is the whole tree as the changes that create it, parents first.
func (t *Tree) Edit() Edit {
	var out Edit
	var walk func(parent string)
	walk = func(parent string) {
		for _, n := range t.Children(parent) {
			c := Change{Op: OpNew, ID: n.ID, Type: n.Type, Parent: n.Parent, Key: n.Key, Attrs: n.Attrs}
			if n.Text != nil {
				c.Text = n.Text.Delta()
			}
			if n.Grid != nil {
				c.Cells = n.Grid.Cells()
			}
			out = append(out, c)
			walk(n.ID)
		}
	}
	walk("")
	return out
}

// Clone is a copy that later edits of t leave alone.
func (t *Tree) Clone() *Tree {
	c := &Tree{nodes: make(map[string]*Node, len(t.nodes)), kids: make(map[string][]string, len(t.kids)), size: t.size}
	for id, n := range t.nodes {
		if n.Text != nil || n.Grid != nil {
			m := *n
			if n.Text != nil {
				m.Text = n.Text.Clone()
			}
			if n.Grid != nil {
				m.Grid = n.Grid.Clone()
			}
			n = &m
		}
		c.nodes[id] = n
	}
	for id, kids := range t.kids {
		c.kids[id] = slices.Clone(kids)
	}
	return c
}

// Apply edits the tree, or leaves it as it was and returns why the edit
// does not apply to it. The edit is well formed: Check accepts it.
func (t *Tree) Apply(e Edit) error {
	if len(e) == 1 {
		return t.apply(e[0], nil)
	}
	var undo []*Node
	var ids []string
	log := func(id string) {
		ids = append(ids, id)
		undo = append(undo, t.nodes[id])
	}
	for _, c := range e {
		if err := t.apply(c, log); err != nil {
			for i := len(ids) - 1; i >= 0; i-- {
				t.put(ids[i], undo[i])
			}
			return err
		}
	}
	return nil
}

// apply makes one change; log, when set, is told of every node about to
// be replaced so that it can be put back.
func (t *Tree) apply(c Change, log func(id string)) error {
	n := t.nodes[c.ID]
	if c.Op == OpNew {
		if n != nil {
			return ErrExists
		}
		if c.Parent != "" && t.nodes[c.Parent] == nil {
			return nil
		}
		n = &Node{ID: c.ID, Type: c.Type, Parent: c.Parent, Key: c.Key, Attrs: c.Attrs}
		if c.Text != nil {
			doc, err := NewDoc(c.Text)
			if err != nil {
				return err
			}
			n.Text = doc
		}
		if c.Cells != nil {
			g, err := newGrid(c.Cells)
			if err != nil {
				return err
			}
			n.Grid = g
		}
		if log != nil {
			log(c.ID)
		}
		t.put(c.ID, n)
		return nil
	}
	if n == nil {
		return nil
	}
	switch c.Op {
	case OpDel:
		ids := []string{c.ID}
		for i := 0; i < len(ids); i++ {
			ids = append(ids, t.kids[ids[i]]...)
		}
		for i := len(ids) - 1; i >= 0; i-- {
			if log != nil {
				log(ids[i])
			}
			t.put(ids[i], nil)
		}
	case OpSet:
		m := *n
		if c.Key != "" {
			m.Key = c.Key
		}
		if len(c.Attrs) > 0 {
			m.Attrs = setValues(n.Attrs, c.Attrs)
		}
		if log != nil {
			log(c.ID)
		}
		t.put(c.ID, &m)
	case OpTxt:
		if n.Text == nil {
			return ErrInvalid
		}
		if log == nil {
			size := n.Text.Len()
			if err := n.Text.Apply(c.Text); err != nil {
				return err
			}
			t.size += n.Text.Len() - size
			return nil
		}
		m := *n
		m.Text = n.Text.Clone()
		if err := m.Text.Apply(c.Text); err != nil {
			return err
		}
		log(c.ID)
		t.put(c.ID, &m)
	case OpCel, OpIns, OpRem:
		if n.Grid == nil {
			return ErrInvalid
		}
		g := n.Grid
		if log != nil {
			m := *n
			m.Grid = n.Grid.Clone()
			g = m.Grid
			log(c.ID)
			t.put(c.ID, &m)
		}
		size := g.Len()
		switch c.Op {
		case OpCel:
			g.set(c.Cells)
		case OpIns:
			g.shift(c.Dim, c.At, c.N)
		case OpRem:
			g.shift(c.Dim, c.At, -c.N)
		}
		t.size += g.Len() - size
	}
	return nil
}

func setValues(old, set Values) Values {
	out := make(Values, len(old)+len(set))
	for k, v := range old {
		out[k] = v
	}
	for k, v := range set {
		if bytes.Equal(v, null) {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// put replaces the node id, nil removing it.
func (t *Tree) put(id string, n *Node) {
	if old := t.nodes[id]; old != nil {
		t.size -= old.len()
		if n == nil {
			kids := t.kids[old.Parent]
			kids = slices.Delete(kids, slices.Index(kids, id), slices.Index(kids, id)+1)
			if len(kids) == 0 {
				delete(t.kids, old.Parent)
			} else {
				t.kids[old.Parent] = kids
			}
		}
	} else if n != nil {
		t.kids[n.Parent] = append(t.kids[n.Parent], id)
	}
	if n == nil {
		delete(t.nodes, id)
		return
	}
	t.nodes[id] = n
	t.size += n.len()
}

func (n *Node) len() int {
	size := 1
	if n.Text != nil {
		size += n.Text.Len()
	}
	if n.Grid != nil {
		size += n.Grid.Len()
	}
	return size
}

// TransformEdit rebases b, made concurrently with a, to apply after a. Text
// changed by both is transformed as by Transform; when both set the same
// attribute or key, the one ordered second wins, as aFirst says.
func TransformEdit(a, b Edit, aFirst bool) Edit {
	a = slices.Clone(a)
	var out Edit
	for _, c := range b {
		for i := range a {
			if c.Op == "" {
				break
			}
			a[i], c = transformChange(a[i], c, aFirst)
		}
		if c.Op != "" {
			out = append(out, c)
		}
	}
	return out
}

// transformChange rebases a and b over each other; a change left with
// nothing to do has no Op.
func transformChange(a, b Change, aFirst bool) (Change, Change) {
	if a.ID == b.ID && grid(a.Op) && grid(b.Op) {
		return transformGrid(a, b, aFirst)
	}
	if a.ID != b.ID || a.Op != b.Op {
		return a, b
	}
	switch a.Op {
	case OpTxt:
		a.Text, b.Text = Transform(b.Text, a.Text, !aFirst), Transform(a.Text, b.Text, aFirst)
		if a.Text == nil {
			a.Op = ""
		}
		if b.Text == nil {
			b.Op = ""
		}
	case OpSet:
		if aFirst {
			a = overridden(a, b)
		} else {
			b = overridden(b, a)
		}
	}
	return a, b
}

// overridden is c without what later sets.
func overridden(c, later Change) Change {
	if later.Key != "" {
		c.Key = ""
	}
	var attrs Values
	for k, v := range c.Attrs {
		if _, set := later.Attrs[k]; !set {
			if attrs == nil {
				attrs = Values{}
			}
			attrs[k] = v
		}
	}
	c.Attrs = attrs
	if c.Key == "" && attrs == nil {
		c.Op = ""
	}
	return c
}

// Growth is how much e can grow a tree at most.
func (e Edit) Growth() int {
	n := 0
	for _, c := range e {
		switch c.Op {
		case OpNew:
			n += 1 + max(0, c.Text.Change()) + len(c.Cells)
		case OpCel:
			n += len(c.Cells)
		case OpTxt:
			n += max(0, c.Text.Change())
		}
	}
	return n
}
