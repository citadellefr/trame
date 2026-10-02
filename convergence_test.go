package trame

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/citadellefr/trame/ot"
	"github.com/citadellefr/trame/trametest"
)

// treeFile is a format for tests that takes any edit: the file is the JSON
// of the nodes.
type treeFile struct{}

func openTree(_ string, data []byte) (*ot.Tree, File, error) {
	var nodes ot.Edit
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, nil, err
	}
	tree, err := ot.NewTree(nodes)
	return tree, treeFile{}, err
}

func (treeFile) Check(*ot.Tree, ot.Edit, Peer) error { return nil }

func (treeFile) Encode(doc *ot.Tree) ([]byte, error) {
	return json.Marshal(doc.Edit())
}

// simClient is the client side of the protocol, as the Dart session runs
// it: its own edits show at once, one is in flight, the others wait
// behind it, and all of them are rebased over what the hub hands out.
type simClient struct {
	t      *testing.T
	id     string
	p      *peer
	outbox [][]byte

	epoch    string // of the document it holds
	joined   string // of the connection
	rev      uint64
	synced   bool
	doc      *ot.Tree
	n        uint64
	inflight ot.Edit
	pending  uint64 // n of the edit in flight, 0 when none
	sent     bool   // whether it went out on this connection
	buffer   ot.Edit
}

func (c *simClient) connect(r *room) {
	c.p = newPeer(trametest.NewConn(), Peer{ID: c.id, Client: c.id}, 1000)
	c.outbox = nil
	c.synced = false
	c.sent = false
	r.join(c.p)
}

func (c *simClient) send(v any) {
	msg, _ := json.Marshal(v)
	c.outbox = append(c.outbox, msg)
}

func (c *simClient) sendInflight() {
	c.sent = true
	c.send(map[string]any{"t": "op", "n": c.pending, "v": c.rev, "d": c.inflight})
}

func (c *simClient) edit(e ot.Edit) {
	c.apply(e)
	if c.pending == 0 && c.synced {
		c.n++
		c.pending, c.inflight = c.n, e
		c.sendInflight()
		return
	}
	c.buffer = append(c.buffer, e...)
}

func (c *simClient) flushBuffer() {
	if c.pending != 0 || c.buffer == nil {
		return
	}
	c.n++
	c.pending, c.inflight, c.buffer = c.n, c.buffer, nil
	c.sendInflight()
}

func (c *simClient) receive(raw []byte) {
	var f trametest.Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		c.t.Fatal(err)
	}
	var e ot.Edit
	if f.D != nil && f.T != "eph" {
		if err := json.Unmarshal(f.D, &e); err != nil {
			c.t.Fatal(err)
		}
	}
	switch f.T {
	case "hello":
		c.send(map[string]any{"t": "sync", "epoch": c.epoch, "v": c.rev})
		c.joined = f.Epoch
	case "doc":
		if c.pending != 0 || c.buffer != nil || c.doc != nil {
			c.t.Fatalf("%s: whole document sent while resuming", c.id)
		}
		doc, err := ot.NewTree(e)
		if err != nil {
			c.t.Fatal(err)
		}
		c.doc, c.rev, c.synced, c.epoch = doc, f.V, true, c.joined
		c.flushBuffer()
	case "ready":
		c.synced, c.epoch = true, c.joined
		if c.pending != 0 && !c.sent {
			c.sendInflight()
		}
		c.flushBuffer()
	case "op":
		if c.pending != 0 {
			c.inflight, e = ot.TransformEdit(e, c.inflight, true), ot.TransformEdit(c.inflight, e, false)
		}
		if c.buffer != nil {
			c.buffer, e = ot.TransformEdit(e, c.buffer, true), ot.TransformEdit(c.buffer, e, false)
		}
		c.apply(e)
		c.rev = f.V
	case "ack":
		if f.N != c.pending {
			c.t.Fatalf("%s: ack of %d, %d in flight", c.id, f.N, c.pending)
		}
		c.rev, c.pending, c.inflight = f.V, 0, nil
		if c.synced {
			c.flushBuffer()
		}
	case "nack":
		c.t.Fatalf("%s: edit refused: %s", c.id, f.Error)
	}
}

func (c *simClient) apply(e ot.Edit) {
	if err := c.doc.Apply(e); err != nil {
		c.t.Fatalf("%s: applying %v: %v", c.id, e, err)
	}
}

// randomEdit adds, deletes, moves and sets nodes, and changes their text:
// inserts, deletes or formats whole characters, never after the last
// paragraph mark nor that mark itself.
func randomEdit(g *rand.Rand, doc *ot.Tree) ot.Edit {
	nodes := doc.Edit()
	if len(nodes) == 0 || g.IntN(10) == 0 {
		parent := ""
		if len(nodes) > 0 && g.IntN(2) == 0 {
			parent = nodes[g.IntN(len(nodes))].ID
		}
		return ot.Edit{{Op: ot.OpNew, ID: fmt.Sprint("n", g.Uint32()), Type: "t", Parent: parent, Key: "V", Text: ot.Delta{{Insert: "new\n"}}}}
	}
	n := nodes[g.IntN(len(nodes))]
	switch k := g.IntN(20); {
	case k == 0:
		return ot.Edit{{Op: ot.OpDel, ID: n.ID}}
	case k < 4:
		return ot.Edit{{Op: ot.OpSet, ID: n.ID, Key: ot.KeyBetween("", n.Key), Attrs: ot.Values{"x": json.RawMessage(fmt.Sprint(g.IntN(9)))}}}
	case n.Text == nil:
		return nil
	}
	var units []rune
	for _, o := range n.Text {
		for _, r := range o.Insert {
			units = append(units, 1+min(r/0x10000, 1))
		}
	}
	units = units[:len(units)-1]
	offset := func(chars int) int {
		n := 0
		for _, u := range units[:chars] {
			n += int(u)
		}
		return n
	}
	at := g.IntN(len(units) + 1)
	span := min(len(units)-at, 1+g.IntN(4))
	d := ot.Delta{}.Push(ot.Op{Retain: offset(at)})
	switch {
	case g.IntN(3) > 0 || span == 0:
		words := []string{"a", "é", "😀", "\n", "bc", " "}
		var b strings.Builder
		for range 1 + g.IntN(3) {
			b.WriteString(words[g.IntN(len(words))])
		}
		d = d.Push(ot.Op{Insert: b.String()})
	case g.IntN(2) == 0:
		d = d.Push(ot.Op{Delete: offset(at+span) - offset(at)})
	default:
		v := []string{"", "1", "2"}[g.IntN(3)]
		d = d.Push(ot.Op{Retain: offset(at+span) - offset(at), Attrs: ot.Attrs{"b": v}})
	}
	return ot.Edit{{Op: ot.OpTxt, ID: n.ID, Text: d}}
}

func TestConvergence(t *testing.T) {
	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { converge(t, seed) })
	}
}

func converge(t *testing.T, seed uint64) {
	g := rand.New(rand.NewPCG(seed, 7))
	store := trametest.NewStore()
	store.Data["c.tree"] = []byte(`[{"o":"new","id":"a","t":"t","k":"V","x":[{"i":"one\ntwo\n"}]},{"o":"new","id":"b","t":"t","p":"a","k":"V"}]`)
	h := NewHub(store, openTree, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour, History: 10_000})
	r, err := h.acquire(context.Background(), "c.tree")
	if err != nil {
		t.Fatal(err)
	}
	defer r.stop()

	clients := make([]*simClient, 4)
	for i := range clients {
		clients[i] = &simClient{t: t, id: fmt.Sprint("c", i)}
		clients[i].connect(r)
	}
	reconnect := func(c *simClient) {
		r.leave(c.p)
		c.connect(r)
	}
	deliverDown := func(c *simClient) bool {
		select {
		case <-c.p.done:
			reconnect(c)
			return true
		default:
		}
		delivered := false
		for {
			select {
			case raw := <-c.p.out:
				c.receive(raw)
				delivered = true
			default:
				return delivered
			}
		}
	}
	deliverUp := func(c *simClient) bool {
		if len(c.outbox) == 0 {
			return false
		}
		msg := c.outbox[0]
		c.outbox = c.outbox[1:]
		r.handle(c.p, msg)
		return true
	}
	for range 3000 {
		c := clients[g.IntN(len(clients))]
		switch k := g.IntN(100); {
		case k < 35:
			if c.doc != nil {
				if e := randomEdit(g, c.doc); e != nil {
					c.edit(e)
				}
			}
		case k < 65:
			deliverUp(c)
		case k < 98:
			deliverDown(c)
		default:
			reconnect(c)
		}
	}
	for busy := true; busy; {
		busy = false
		for _, c := range clients {
			for deliverUp(c) || deliverDown(c) {
				busy = true
			}
		}
	}

	r.mu.Lock()
	want := r.doc.Edit()
	r.mu.Unlock()
	for _, c := range clients {
		if c.pending != 0 || c.buffer != nil {
			t.Fatalf("%s still has edits to send", c.id)
		}
		if got := c.doc.Edit(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s has %v, the hub %v", c.id, got, want)
		}
	}
}
