package ot

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"math/rand/v2"
	"os"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the vectors in testdata/ot")

func ins(s string, attrs ...string) Op {
	return Op{Insert: s, Attrs: attrsOf(attrs)}
}

func ret(n int, attrs ...string) Op {
	return Op{Retain: n, Attrs: attrsOf(attrs)}
}

func del(n int) Op {
	return Op{Delete: n}
}

func attrsOf(kv []string) Attrs {
	if len(kv) == 0 {
		return nil
	}
	a := Attrs{}
	for i := 0; i < len(kv); i += 2 {
		a[kv[i]] = kv[i+1]
	}
	return a
}

func compose(t *testing.T, a, b Delta) Delta {
	t.Helper()
	out, err := Compose(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPush(t *testing.T) {
	for _, c := range []struct {
		ops  []Op
		want Delta
	}{
		{[]Op{ins("a"), ins("b")}, Delta{ins("ab")}},
		{[]Op{ins("a", "b", "1"), ins("b")}, Delta{ins("a", "b", "1"), ins("b")}},
		{[]Op{ret(2), ret(3)}, Delta{ret(5)}},
		{[]Op{del(2), del(3)}, Delta{del(5)}},
		{[]Op{ret(1), del(2), ins("x")}, Delta{ret(1), ins("x"), del(2)}},
		{[]Op{del(2), ins("x")}, Delta{ins("x"), del(2)}},
		{[]Op{ins("a"), del(2), ins("b")}, Delta{ins("ab"), del(2)}},
		{[]Op{ins(""), ret(0), del(0)}, nil},
		{[]Op{ins("a", "b", "")}, Delta{ins("a", "b", "")}},
		{[]Op{ret(1, "b", "1"), ret(1, "b", "1"), ret(1)}, Delta{ret(2, "b", "1"), ret(1)}},
	} {
		var d Delta
		for _, o := range c.ops {
			d = d.Push(o)
		}
		if !reflect.DeepEqual(d, c.want) {
			t.Errorf("%v: got %v, want %v", c.ops, d, c.want)
		}
	}
}

func TestCompose(t *testing.T) {
	for _, c := range []struct{ a, b, want Delta }{
		{Delta{ins("abc\n")}, Delta{ret(1), ins("x")}, Delta{ins("axbc\n")}},
		{Delta{ins("abc\n")}, Delta{ret(1), del(1)}, Delta{ins("ac\n")}},
		{Delta{ins("abc\n")}, Delta{ret(3, "b", "1")}, Delta{ins("abc", "b", "1"), ins("\n")}},
		{Delta{ins("ab", "b", "1")}, Delta{ret(1, "b", "")}, Delta{ins("a"), ins("b", "b", "1")}},
		{Delta{ret(2, "b", "1")}, Delta{ret(2, "b", "")}, Delta{ret(2, "b", "")}},
		{Delta{ret(1), ins("x")}, Delta{ret(1), del(1)}, nil},
		{Delta{del(1)}, Delta{ins("x")}, Delta{ins("x"), del(1)}},
		{Delta{ins("😀\n")}, Delta{ret(2), ins("!")}, Delta{ins("😀!\n")}},
	} {
		if got := compose(t, c.a, c.b); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Compose(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if _, err := Compose(Delta{ins("😀\n")}, Delta{ret(1), ins("!")}); !errors.Is(err, ErrSplit) {
		t.Errorf("splitting a surrogate pair: %v", err)
	}
}

func TestTransform(t *testing.T) {
	for _, c := range []struct {
		name   string
		a, b   Delta
		aFirst bool
		want   Delta
	}{
		{"insert after", Delta{ins("x")}, Delta{ret(2), ins("y")}, true, Delta{ret(3), ins("y")}},
		{"same place, a first", Delta{ret(1), ins("x")}, Delta{ret(1), ins("y")}, true, Delta{ret(2), ins("y")}},
		{"same place, b first", Delta{ret(1), ins("x")}, Delta{ret(1), ins("y")}, false, Delta{ret(1), ins("y")}},
		{"typed in deleted text", Delta{ret(1), del(3)}, Delta{ret(2), ins("y")}, true, Delta{ret(1), ins("y")}},
		{"both deleted", Delta{del(3)}, Delta{ret(1), del(1)}, true, nil},
		{"format of deleted", Delta{del(2)}, Delta{ret(4, "b", "1")}, true, Delta{ret(2, "b", "1")}},
		{"format conflict, a first", Delta{ret(2, "b", "1")}, Delta{ret(2, "b", "")}, true, nil},
		{"format conflict, b first", Delta{ret(2, "b", "1")}, Delta{ret(2, "b", "")}, false, Delta{ret(2, "b", "")}},
		{"format kept", Delta{ret(2, "b", "1")}, Delta{ret(2, "b", "", "i", "1")}, true, Delta{ret(2, "i", "1")}},
	} {
		if got := Transform(c.a, c.b, c.aFirst); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Enter pressed in a paragraph while someone types further along it: the
// text they type follows its paragraph.
func TestSplitWhileTyping(t *testing.T) {
	doc := Delta{ins("hello world\n")}
	enter := Delta{ret(5), ins("\n")}
	typing := Delta{ret(11), ins("!")}
	one := compose(t, compose(t, doc, enter), Transform(enter, typing, true))
	two := compose(t, compose(t, doc, typing), Transform(typing, enter, false))
	want := Delta{ins("hello\n world!\n")}
	if !reflect.DeepEqual(one, want) || !reflect.DeepEqual(two, want) {
		t.Errorf("got %v and %v, want %v", one, two, want)
	}
}

func TestTransformPosition(t *testing.T) {
	for _, c := range []struct {
		d      Delta
		pos    int
		dFirst bool
		want   int
	}{
		{Delta{ins("ab")}, 0, true, 2},
		{Delta{ins("ab")}, 0, false, 0},
		{Delta{ret(1), ins("ab")}, 3, false, 5},
		{Delta{ret(1), del(2)}, 2, false, 1},
		{Delta{ret(1), del(2)}, 5, false, 3},
		{Delta{ret(5), ins("x")}, 2, true, 2},
	} {
		if got := TransformPosition(c.d, c.pos, c.dFirst); got != c.want {
			t.Errorf("TransformPosition(%v, %d, %v) = %d, want %d", c.d, c.pos, c.dFirst, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	for _, d := range []Delta{
		{{}},
		{{Insert: "a", Retain: 1}},
		{{Delete: -1}},
		{{Retain: -2}},
		{{Delete: 1, Attrs: Attrs{"b": "1"}}},
		{{Insert: "a", Attrs: Attrs{"b": ""}}},
		{{Retain: 1, Attrs: Attrs{"": "1"}}},
	} {
		if d.Check() == nil {
			t.Errorf("%v passes", d)
		}
	}
	if err := (Delta{ret(1, "b", ""), ins("a", "b", "1"), del(2)}).Check(); err != nil {
		t.Error(err)
	}
}

func TestDocApply(t *testing.T) {
	doc, err := NewDoc(Delta{ins("one\ntwo\n"), ins("three\n", "h", "1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		d    Delta
		want error
	}{
		{Delta{ret(14), ins("x")}, ErrNoMark},
		{Delta{ret(13), del(1)}, ErrNoMark},
		{Delta{ret(10), del(6)}, ErrLength},
		{Delta{ret(15)}, ErrLength},
		{Delta{ret(10), ret(4)}, nil},
		{Delta{ret(14), ins("four\n")}, nil},
	} {
		if err := doc.Clone().Apply(c.d); err != c.want {
			t.Errorf("Apply(%v) = %v, want %v", c.d, err, c.want)
		}
	}
	if err := doc.Apply(Delta{ret(3), del(1), ret(1), ins("\n")}); err != nil {
		t.Fatal(err)
	}
	want := Delta{ins("onet\nwo\n"), ins("three\n", "h", "1")}
	if got := doc.Delta(); !reflect.DeepEqual(got, want) || doc.Len() != 14 || len(doc.Paragraphs()) != 3 {
		t.Errorf("got %v (%d, %d paragraphs), want %v", got, doc.Len(), len(doc.Paragraphs()), want)
	}
}

// Random flows and deltas, in the characters where the Dart side differs from
// Go: surrogate pairs, and paragraph marks.
type generator struct{ *rand.Rand }

var alphabet = []string{"a", "b", " ", "é", "中", "😀", "\n"}

func (g generator) text() string {
	var b strings.Builder
	for range 1 + g.IntN(4) {
		b.WriteString(alphabet[g.IntN(len(alphabet))])
	}
	return b.String()
}

func (g generator) attrs(removals bool) Attrs {
	if g.IntN(3) > 0 {
		return nil
	}
	a := Attrs{}
	for _, k := range []string{"b", "i", "s"} {
		switch g.IntN(4) {
		case 0:
			a[k] = "1"
		case 1:
			a[k] = "2"
		case 2:
			if removals {
				a[k] = ""
			}
		}
	}
	return a
}

func (g generator) flow() Delta {
	var d Delta
	for range g.IntN(6) {
		d = d.Push(Op{Insert: g.text(), Attrs: g.attrs(false)})
	}
	return d.Push(Op{Insert: "\n", Attrs: g.attrs(false)})
}

// delta edits a flow, stepping over whole characters.
func (g generator) delta(flow Delta) Delta {
	var chars []int
	for _, o := range flow {
		for _, r := range o.Insert {
			chars = append(chars, utf16Len(string(r)))
		}
	}
	var d Delta
	for i := 0; i < len(chars) && g.IntN(8) > 0; {
		n := 0
		for k := 1 + g.IntN(3); k > 0 && i < len(chars); k-- {
			n += chars[i]
			i++
		}
		switch g.IntN(4) {
		case 0:
			d = d.Push(Op{Retain: n, Attrs: g.attrs(true)})
		case 1:
			d = d.Push(Op{Delete: n})
		case 2:
			d = d.Push(Op{Retain: n})
		default:
			d = d.Push(Op{Insert: g.text(), Attrs: g.attrs(false)})
			d = d.Push(Op{Retain: n})
		}
	}
	if g.IntN(4) == 0 {
		d = d.Push(Op{Insert: g.text(), Attrs: g.attrs(false)})
	}
	return d
}

func TestRandom(t *testing.T) {
	g := generator{rand.New(rand.NewPCG(1, 2))}
	for range 20_000 {
		doc := g.flow()
		a, b := g.delta(doc), g.delta(doc)

		ab := compose(t, compose(t, doc, a), Transform(a, b, true))
		ba := compose(t, compose(t, doc, b), Transform(b, a, false))
		if !reflect.DeepEqual(ab, ba) {
			t.Fatalf("doc %v, a %v, b %v: %v then %v", doc, a, b, ab, ba)
		}

		afterA := compose(t, doc, a)
		c := g.delta(afterA)
		if got, want := compose(t, afterA, c), compose(t, doc, compose(t, a, c)); !reflect.DeepEqual(got, want) {
			t.Fatalf("doc %v, a %v, c %v: stepwise %v, composed %v", doc, a, c, got, want)
		}

		d, err := NewDoc(doc)
		if err != nil {
			t.Fatal(err)
		}
		want := compose(t, doc, a)
		err = d.Apply(a)
		ends := len(want) > 0 && strings.HasSuffix(want[len(want)-1].Insert, "\n") && !deletesLast(a, doc.Change())
		switch {
		case !ends && !errors.Is(err, ErrNoMark):
			t.Fatalf("doc %v, a %v: applied to %v without its mark (%v)", doc, a, d.Delta(), err)
		case ends && err != nil:
			t.Fatalf("doc %v, a %v: %v", doc, a, err)
		case ends && !reflect.DeepEqual(d.Delta(), want):
			t.Fatalf("doc %v, a %v: applied %v, composed %v", doc, a, d.Delta(), want)
		}
	}
}

// The vectors the Dart package replays, to run the same algorithms as Go.
type vector struct {
	Kind  string `json:"kind"`
	A     Delta  `json:"a"`
	B     Delta  `json:"b,omitempty"`
	First bool   `json:"first,omitempty"`
	Pos   int    `json:"pos,omitempty"`
	Out   any    `json:"out"`
}

func TestVectors(t *testing.T) {
	g := generator{rand.New(rand.NewPCG(3, 4))}
	var vectors []vector
	for range 300 {
		doc := g.flow()
		a, b := g.delta(doc), g.delta(doc)
		first := g.IntN(2) == 0
		vectors = append(vectors,
			vector{Kind: "compose", A: doc, B: a, Out: compose(t, doc, a)},
			vector{Kind: "compose", A: a, B: g.delta(compose(t, doc, a)), Out: nil},
			vector{Kind: "transform", A: a, B: b, First: first, Out: Transform(a, b, first)},
		)
		v := &vectors[len(vectors)-2]
		v.Out = compose(t, v.A, v.B)
		pos := g.IntN(doc.Change() + 1)
		vectors = append(vectors, vector{Kind: "position", A: a, Pos: pos, First: first, Out: TransformPosition(a, pos, first)})
	}
	writeVectors(t, "../testdata/ot/vectors.json", vectors)
}

// writeVectors checks path holds vectors, one per line, or writes it with
// -update.
func writeVectors[V any](t *testing.T, path string, vectors []V) {
	t.Helper()
	data := []byte("[")
	for i, v := range vectors {
		line, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			data = append(data, ',')
		}
		data = append(append(data, '\n'), line...)
	}
	data = append(data, "\n]\n"...)
	if *update {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(old, data) {
		t.Fatalf("%s is stale: go test ./ot -run Vectors -update", path)
	}
}
