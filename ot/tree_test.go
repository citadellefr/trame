package ot

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

func mustTree(t *testing.T, nodes Edit) *Tree {
	t.Helper()
	tree, err := NewTree(nodes)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func values(kv ...string) Values {
	v := Values{}
	for i := 0; i < len(kv); i += 2 {
		v[kv[i]] = json.RawMessage(kv[i+1])
	}
	return v
}

func TestKeys(t *testing.T) {
	for _, n := range []int{1, 2, 61, 62, 63, 1000, 5000} {
		keys := Keys(n)
		if !slices.IsSorted(keys) || len(slices.Compact(slices.Clone(keys))) != n {
			t.Fatalf("Keys(%d) not strictly increasing: %v", n, keys)
		}
		for _, k := range keys {
			if !name(k) || k[len(k)-1] == '0' {
				t.Fatalf("Keys(%d): bad key %q", n, k)
			}
		}
	}
	g := rand.New(rand.NewPCG(5, 6))
	keys := []string{KeyBetween("", "")}
	for range 2000 {
		i := g.IntN(len(keys) + 1)
		a, b := "", ""
		if i > 0 {
			a = keys[i-1]
		}
		if i < len(keys) {
			b = keys[i]
		}
		k := KeyBetween(a, b)
		if k <= a || b != "" && k >= b || k[len(k)-1] == '0' {
			t.Fatalf("KeyBetween(%q, %q) = %q", a, b, k)
		}
		keys = slices.Insert(keys, i, k)
	}
}

func TestTreeApply(t *testing.T) {
	tree := mustTree(t, Edit{
		{Op: OpNew, ID: "s1", Type: "slide", Key: "V"},
		{Op: OpNew, ID: "a", Type: "shape", Parent: "s1", Key: "V", Attrs: values("x", "1"), Text: Delta{ins("ab\n")}},
		{Op: OpNew, ID: "b", Type: "shape", Parent: "s1", Key: "F"},
	})
	if got := tree.Children("s1"); len(got) != 2 || got[0].ID != "b" || tree.Len() != 6 {
		t.Fatalf("children %v, size %d", got, tree.Len())
	}

	before := tree.Edit()
	for _, c := range []struct {
		e    Edit
		want error
	}{
		{Edit{{Op: OpTxt, ID: "a", Text: Delta{ret(1), ins("x")}}, {Op: OpTxt, ID: "a", Text: Delta{ret(5)}}}, ErrLength},
		{Edit{{Op: OpDel, ID: "s1"}, {Op: OpNew, ID: "c", Type: "shape"}, {Op: OpTxt, ID: "b", Text: Delta{ins("x")}}, {Op: OpNew, ID: "c", Type: "shape"}}, ErrExists},
		{Edit{{Op: OpSet, ID: "a", Attrs: values("x", "2")}, {Op: OpTxt, ID: "b", Text: Delta{ins("x")}}}, ErrInvalid},
	} {
		if err := tree.Apply(c.e); err != c.want {
			t.Errorf("Apply(%v) = %v, want %v", c.e, err, c.want)
		}
		if !reflect.DeepEqual(tree.Edit(), before) || tree.Len() != 6 {
			t.Fatalf("Apply(%v) left %v", c.e, tree.Edit())
		}
	}

	err := tree.Apply(Edit{
		{Op: OpSet, ID: "a", Key: "0V", Attrs: values("x", "null", "y", `"z"`)},
		{Op: OpTxt, ID: "a", Text: Delta{ret(2), ins("\nc")}},
		{Op: OpSet, ID: "gone", Attrs: values("x", "1")},
		{Op: OpNew, ID: "orphan", Type: "shape", Parent: "gone"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := tree.Node("a")
	if got := tree.Children("s1"); got[0] != a || !reflect.DeepEqual(a.Attrs, values("y", `"z"`)) ||
		len(a.Text.Paragraphs()) != 2 || tree.Node("orphan") != nil || tree.Len() != 8 {
		t.Fatalf("got %+v, size %d", a, tree.Len())
	}

	if err := tree.Apply(Edit{{Op: OpDel, ID: "s1"}}); err != nil || tree.Len() != 0 || len(tree.Edit()) != 0 {
		t.Fatalf("after deleting the slide: %v, %v", err, tree.Edit())
	}
}

func TestEditCheck(t *testing.T) {
	for _, e := range []Edit{
		{{Op: "mov", ID: "a"}},
		{{Op: OpDel, ID: ""}},
		{{Op: OpDel, ID: "a b"}},
		{{Op: OpDel, ID: "a", Key: "V"}},
		{{Op: OpNew, ID: "a", Key: "V"}},
		{{Op: OpNew, ID: "a", Type: "t"}},
		{{Op: OpNew, ID: "a", Type: "t", Key: "V", Text: Delta{ins("no mark")}}},
		{{Op: OpNew, ID: "a", Type: "t", Key: "V", Attrs: values("x", "null")}},
		{{Op: OpSet, ID: "a"}},
		{{Op: OpSet, ID: "a", Key: "é"}},
		{{Op: OpTxt, ID: "a", Text: Delta{{Retain: -1}}}},
	} {
		if e.Check() == nil {
			t.Errorf("%v passes", e)
		}
	}
}

// treeGen makes random trees and edits of them: few ids, so that edits
// meet on the same nodes.
type treeGen struct {
	generator
	next int
}

func (g *treeGen) id() string {
	g.next++
	return fmt.Sprintf("n%d", g.next)
}

func (g *treeGen) value() json.RawMessage {
	return json.RawMessage([]string{"1", "2", `"a"`, "[1,2]", `{"b":true}`}[g.IntN(5)])
}

func (g *treeGen) node(parent string) Change {
	c := Change{Op: OpNew, ID: g.id(), Type: "t", Parent: parent, Key: KeyBetween("", "")}
	if g.IntN(2) == 0 {
		c.Key = Keys(3)[g.IntN(3)]
	}
	if g.IntN(2) == 0 {
		c.Attrs = Values{"x": g.value()}
	}
	switch g.IntN(4) {
	case 0:
		c.Cells = g.cells(false)
	case 1, 2:
		c.Text = g.flow()
	}
	return c
}

func (g *treeGen) tree() Edit {
	var e Edit
	for range 1 + g.IntN(3) {
		top := g.node("")
		e = append(e, top)
		for range g.IntN(3) {
			e = append(e, g.node(top.ID))
		}
	}
	return e
}

// edit changes tree, which it applies itself to so that every change of the
// edit is made on the tree as the previous ones left it.
func (g *treeGen) edit(tree *Tree) Edit {
	var e Edit
	for range 1 + g.IntN(3) {
		nodes := tree.Edit()
		var c Change
		switch n := g.IntN(10); {
		case n == 0 || len(nodes) == 0:
			parent := ""
			if len(nodes) > 0 && g.IntN(2) == 0 {
				parent = nodes[g.IntN(len(nodes))].ID
			}
			c = g.node(parent)
		case n == 1:
			c = Change{Op: OpDel, ID: nodes[g.IntN(len(nodes))].ID}
		case n < 5:
			c = Change{Op: OpSet, ID: nodes[g.IntN(len(nodes))].ID, Attrs: Values{}}
			for _, k := range []string{"x", "y"} {
				switch g.IntN(3) {
				case 0:
					c.Attrs[k] = g.value()
				case 1:
					c.Attrs[k] = json.RawMessage("null")
				}
			}
			if g.IntN(2) == 0 || len(c.Attrs) == 0 {
				c.Key = Keys(5)[g.IntN(5)]
			}
			if len(c.Attrs) == 0 {
				c.Attrs = nil
			}
		case n < 7:
			var grids []Change
			for _, n := range nodes {
				if n.Cells != nil {
					grids = append(grids, n)
				}
			}
			if len(grids) == 0 {
				continue
			}
			c = g.gridChange(grids[g.IntN(len(grids))].ID)
		default:
			var texts []Change
			for _, n := range nodes {
				if n.Text != nil {
					texts = append(texts, n)
				}
			}
			if len(texts) == 0 {
				continue
			}
			n := texts[g.IntN(len(texts))]
			c = Change{Op: OpTxt, ID: n.ID, Text: g.delta(n.Text)}
		}
		if tree.Apply(Edit{c}) == nil {
			e = append(e, c)
		}
	}
	return e
}

func applied(t *testing.T, nodes Edit, edits ...Edit) Edit {
	t.Helper()
	tree := mustTree(t, nodes)
	for _, e := range edits {
		if err := tree.Apply(e); err != nil {
			t.Fatalf("applying %v to %v: %v", e, nodes, err)
		}
	}
	return tree.Edit()
}

func TestTreeRandom(t *testing.T) {
	g := &treeGen{generator: generator{rand.New(rand.NewPCG(7, 8))}}
	for range 10_000 {
		nodes := g.tree()
		a := g.edit(mustTree(t, nodes))
		b := g.edit(mustTree(t, nodes))
		for _, e := range []Edit{a, b} {
			if err := e.Check(); err != nil {
				t.Fatalf("%v: %v", e, err)
			}
		}
		ab := applied(t, nodes, a, TransformEdit(a, b, true))
		ba := applied(t, nodes, b, TransformEdit(b, a, false))
		if !reflect.DeepEqual(ab, ba) {
			t.Fatalf("tree %v\na %v\nb %v:\n%v\nthen\n%v", nodes, a, b, ab, ba)
		}
	}
}

func BenchmarkTreeTyping(b *testing.B) {
	var nodes Edit
	keys := Keys(2000)
	for i, k := range keys {
		nodes = append(nodes, Change{Op: OpNew, ID: fmt.Sprint("n", i), Type: "shape", Key: k, Text: Delta{ins("some text in a shape\n")}})
	}
	tree, err := NewTree(nodes)
	if err != nil {
		b.Fatal(err)
	}
	typed := Edit{{Op: OpTxt, ID: "n1000", Text: Delta{ret(4), ins("x")}}}
	erased := Edit{{Op: OpTxt, ID: "n1000", Text: Delta{ret(4), del(1)}}}
	for b.Loop() {
		if err := tree.Apply(typed); err != nil {
			b.Fatal(err)
		}
		if err := tree.Apply(erased); err != nil {
			b.Fatal(err)
		}
	}
}

// treeVector is a case the Dart package replays: a tree, two edits made on
// it at the same time, b rebased over a, and the tree after a then b.
type treeVector struct {
	Kind  string   `json:"kind"`
	Nodes Edit     `json:"nodes,omitempty"`
	A     Edit     `json:"a,omitempty"`
	B     Edit     `json:"b,omitempty"`
	First bool     `json:"first,omitempty"`
	Keys  []string `json:"keys,omitempty"`
	Out   any      `json:"out"`
}

func TestTreeVectors(t *testing.T) {
	g := &treeGen{generator: generator{rand.New(rand.NewPCG(9, 10))}}
	var vectors []treeVector
	for range 300 {
		nodes := g.tree()
		a := g.edit(mustTree(t, nodes))
		b := g.edit(mustTree(t, nodes))
		first := g.IntN(2) == 0
		rebased := TransformEdit(a, b, first)
		vectors = append(vectors,
			treeVector{Kind: "transform", Nodes: nodes, A: a, B: b, First: first, Out: rebased},
			treeVector{Kind: "apply", Nodes: nodes, A: a, B: rebased, Out: applied(t, nodes, a, rebased)},
		)
	}
	keys := []string{"", ""}
	for range 200 {
		i := g.IntN(len(keys) - 1)
		k := KeyBetween(keys[i], keys[i+1])
		vectors = append(vectors, treeVector{Kind: "key", Keys: []string{keys[i], keys[i+1]}, Out: k})
		keys = slices.Insert(keys, i+1, k)
	}
	writeVectors(t, "../testdata/ot/tree.json", vectors)
}
