package trame

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/citadellefr/trame/ot"
	"github.com/citadellefr/trame/trametest"
)

func connect(t *testing.T, h *Hub, ctx context.Context, key string, info Peer) *trametest.Client {
	t.Helper()
	return trametest.Connect(t, func(c *trametest.Conn) error { return h.Serve(ctx, c, key, info) })
}

func join(t *testing.T, h *Hub, key string, info Peer) (*trametest.Client, trametest.Frame, trametest.Frame) {
	t.Helper()
	return trametest.Join(t, func(c *trametest.Conn) error { return h.Serve(context.Background(), c, key, info) })
}

func fastOptions() Options {
	return Options{SaveDelay: 20 * time.Millisecond, SaveMaxDelay: 100 * time.Millisecond}
}

func TestEditsAreRebasedAndSaved(t *testing.T) {
	store := trametest.NewStore()
	store.Data["a.txt"] = []byte("one\r\ntwo")
	h := NewHub(store, Text, fastOptions())

	alice, hello, doc := join(t, h, "a.txt", Peer{ID: "1", Name: "Alice", Client: "ca"})
	if hello.SID != 1 || hello.ID != "1" || hello.Name != "Alice" || len(hello.Peers) != 0 || hello.Epoch == "" {
		t.Fatalf("hello = %+v", hello)
	}
	if string(doc.D) != `[{"o":"new","id":"body","t":"text","k":"V","x":[{"i":"one\ntwo\n"}]}]` || doc.V != 0 {
		t.Fatalf("doc = %+v", doc)
	}
	bob, hello, _ := join(t, h, "a.txt", Peer{ID: "2", Name: "Bob"})
	if len(hello.Peers) != 1 || hello.Peers[0].Name != "Alice" {
		t.Fatalf("hello = %+v", hello)
	}
	if f := alice.Expect("join"); f.Peer.Name != "Bob" || f.Peer.SID != 2 {
		t.Fatalf("join = %+v", f)
	}

	// both typed on revision 0: Bob's edit comes second and is rebased
	alice.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"X"}]}]}`)
	if f := alice.Expect("ack"); f.N != 1 || f.V != 1 {
		t.Fatalf("ack = %+v", f)
	}
	bob.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"r":3},{"i":"Y"}]}]}`)
	if f := bob.Expect("op"); f.SID != 1 || f.V != 1 || string(f.D) != `[{"o":"txt","id":"body","x":[{"i":"X"}]}]` {
		t.Fatalf("op = %+v", f)
	}
	if f := bob.Expect("ack"); f.V != 2 {
		t.Fatalf("ack = %+v", f)
	}
	if f := alice.Expect("op"); f.SID != 2 || f.V != 2 || string(f.D) != `[{"o":"txt","id":"body","x":[{"r":4},{"i":"Y"}]}]` {
		t.Fatalf("op = %+v", f)
	}

	<-store.Saves
	if got := store.File("a.txt"); got != "XoneY\r\ntwo" {
		t.Fatalf("saved %q", got)
	}
	if f := alice.Expect("saved"); f.V != 2 {
		t.Fatalf("saved = %+v", f)
	}
	bob.Expect("saved")

	bob.Leave()
	alice.Expect("leave")
}

func TestWholeDocumentFollowsEdits(t *testing.T) {
	h := NewHub(trametest.NewStore(), Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	alice, _, first := join(t, h, "a.txt", Peer{ID: "1", Client: "ca"})
	_, _, second := join(t, h, "a.txt", Peer{ID: "2"})
	alice.Expect("join")
	if string(second.D) != string(first.D) {
		t.Fatalf("doc = %s, want %s", second.D, first.D)
	}
	alice.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"X"}]}]}`)
	alice.Expect("ack")
	_, _, third := join(t, h, "a.txt", Peer{ID: "3"})
	if third.V != 1 || string(third.D) != `[{"o":"new","id":"body","t":"text","k":"V","x":[{"i":"X\n"}]}]` {
		t.Fatalf("doc = %+v %s", third, third.D)
	}
}

func TestReconnectCatchesUp(t *testing.T) {
	h := NewHub(trametest.NewStore(), Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	keeper, hello, _ := join(t, h, "a.txt", Peer{ID: "0"})
	epoch := hello.Epoch

	first, _, _ := join(t, h, "a.txt", Peer{ID: "1", Client: "c1"})
	keeper.Expect("join")
	keeper.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"k"}]}]}`)
	keeper.Expect("ack")
	first.Expect("op")
	first.Send(`{"t":"op","n":1,"v":1,"d":[{"o":"txt","id":"body","x":[{"i":"a"}]}]}`)
	first.Expect("ack")
	first.Leave()
	keeper.Expect("op")
	keeper.Expect("leave")
	keeper.Send(`{"t":"op","n":2,"v":2,"d":[{"o":"txt","id":"body","x":[{"i":"b"}]}]}`)
	keeper.Expect("ack")

	// back from revision 1: its own edit is acknowledged, the other replayed
	again := connect(t, h, context.Background(), "a.txt", Peer{ID: "1", Client: "c1"})
	if f := again.Expect("hello"); f.V != 3 || f.Epoch != epoch {
		t.Fatalf("hello = %+v", f)
	}
	again.Send(`{"t":"sync","epoch":"` + epoch + `","v":1}`)
	if f := again.Expect("ack"); f.N != 1 || f.V != 2 {
		t.Fatalf("ack = %+v", f)
	}
	if f := again.Expect("op"); f.V != 3 || string(f.D) != `[{"o":"txt","id":"body","x":[{"i":"b"}]}]` {
		t.Fatalf("op = %+v", f)
	}
	if f := again.Expect("ready"); f.V != 3 {
		t.Fatalf("ready = %+v", f)
	}
	again.Send(`{"t":"op","n":1,"v":1,"d":[{"o":"txt","id":"body","x":[{"i":"z"}]}]}`)
	again.Send(`{"t":"op","n":2,"v":3,"d":[{"o":"txt","id":"body","x":[{"r":3},{"i":"c"}]}]}`)
	if f := again.Expect("ack"); f.N != 2 || f.V != 4 {
		t.Fatalf("ack = %+v", f)
	}

	// from another stay in memory, or too far back: the whole document
	for _, sync := range []string{`{"t":"sync","epoch":"other","v":1}`, `{"t":"sync","epoch":"` + epoch + `","v":9}`} {
		c := connect(t, h, context.Background(), "a.txt", Peer{ID: "1", Client: "c1"})
		c.Expect("hello")
		c.Send(sync)
		if f := c.Expect("doc"); f.V != 4 || f.Ack != 2 || string(f.D) != `[{"o":"new","id":"body","t":"text","k":"V","x":[{"i":"bakc\n"}]}]` {
			t.Fatalf("doc = %+v", f)
		}
		c.Leave()
	}
}

func TestRefusedOperations(t *testing.T) {
	store := trametest.NewStore()
	store.Data["a.txt"] = []byte("abc")
	h := NewHub(store, Text, Options{MaxLength: 8, History: 2})

	reader, _, _ := join(t, h, "a.txt", Peer{ID: "1", ReadOnly: true})
	reader.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"x"}]}]}`)
	if f := reader.Expect("nack"); f.N != 1 || f.Error != errReadOnly.Error() {
		t.Fatalf("nack = %+v", f)
	}

	writer, _, _ := join(t, h, "a.txt", Peer{ID: "2"})
	reader.Expect("join")
	for _, c := range []struct{ op, err string }{
		{`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"x","r":1}]}]}`, errMalformed.Error()},
		{`{"t":"op","n":2,"v":0,"d":[{"o":"txt","id":"body","x":[{"d":-1}]}]}`, errMalformed.Error()},
		{`{"t":"op","n":3,"v":0,"d":{"i":"x"}}`, errMalformed.Error()},
		{`{"t":"op","n":4,"v":0,"d":[{"o":"txt","id":"body","x":[{"r":4},{"i":"x"}]}]}`, ot.ErrNoMark.Error()},
		{`{"t":"op","n":5,"v":0,"d":[{"o":"txt","id":"body","x":[{"r":9}]}]}`, ot.ErrLength.Error()},
		{`{"t":"op","n":6,"v":1,"d":[{"o":"txt","id":"body","x":[{"i":"x"}]}]}`, errStale.Error()},
		{`{"t":"op","n":7,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"12345"}]}]}`, errTooLong.Error()},
	} {
		writer.Send(c.op)
		if f := writer.Expect("nack"); f.Error != c.err {
			t.Errorf("%s: nack %q, want %q", c.op, f.Error, c.err)
		}
	}
	reader.Quiet()

	for n := range 3 {
		writer.Send(`{"t":"op","n":` + strconv.Itoa(8+n) + `,"v":` + strconv.Itoa(n) + `,"d":[{"o":"txt","id":"body","x":[{"d":1}]}]}`)
		writer.Expect("ack")
	}
	writer.Send(`{"t":"op","n":11,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"x"}]}]}`)
	if f := writer.Expect("nack"); f.Error != errStale.Error() {
		t.Fatalf("an edit older than the history: %+v", f)
	}
}

func TestEditsBeforeSyncAreIgnored(t *testing.T) {
	h := NewHub(trametest.NewStore(), Text, Options{})
	c := connect(t, h, context.Background(), "a.txt", Peer{ID: "1"})
	c.Expect("hello")
	c.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"x"}]}]}`)
	c.Send(`{"t":"sync"}`)
	if f := c.Expect("doc"); string(f.D) != `[{"o":"new","id":"body","t":"text","k":"V","x":[{"i":"\n"}]}]` {
		t.Fatalf("doc = %+v", f)
	}
	c.Quiet()
}

func TestPresenceIsRelayedAndRateLimited(t *testing.T) {
	h := NewHub(trametest.NewStore(), Text, Options{PresenceRate: 3})
	a, _, _ := join(t, h, "a.txt", Peer{ID: "1"})
	b, _, _ := join(t, h, "a.txt", Peer{ID: "2"})
	a.Expect("join")

	for range 10 {
		a.Send(`{"t":"eph","d":{"s":[1,2]}}`)
	}
	for range 3 {
		if f := b.Expect("eph"); f.SID != 1 || string(f.D) != `{"s":[1,2]}` {
			t.Fatalf("eph = %+v", f)
		}
	}
	b.Quiet()
	a.Quiet()
	b.Send(`{"d":{"s":[3,4]},"t":"eph"}`)
	if f := a.Expect("eph"); string(f.D) != `{"s":[3,4]}` {
		t.Fatalf("eph = %+v", f)
	}
}

func TestPresenceData(t *testing.T) {
	for _, c := range []struct {
		msg, d string
		ok     bool
	}{
		{`{"t":"eph","d":{"s":[1,2]}}`, `{"s":[1,2]}`, true},
		{`{"t":"eph","d":null}`, `null`, true},
		{`{"t":"eph","d":{"s":[1,2]},"x":1}`, "", false},
		{`{"t":"eph","d":{"s":[1,2}}`, "", false},
		{`{"d":{"s":[1,2]},"t":"eph"}`, "", false},
		{`{"t":"op","n":1}`, "", false},
	} {
		d, ok := presenceData([]byte(c.msg))
		if ok != c.ok || ok && string(d) != c.d {
			t.Errorf("presenceData(%s) = %s, %v", c.msg, d, ok)
		}
	}
}

type compressingConn struct {
	*trametest.Conn
	sizes chan int
	next  bool
}

func (c *compressingConn) EnableWriteCompression(enable bool) { c.next = enable }

func (c *compressingConn) WriteMessage(kind int, data []byte) error {
	if c.next {
		c.sizes <- len(data)
	}
	return c.Conn.WriteMessage(kind, data)
}

func TestOnlyLargeFramesAreCompressed(t *testing.T) {
	store := trametest.NewStore()
	store.Data["a.txt"] = []byte(strings.Repeat("x", compressFrom))
	h := NewHub(store, Text, Options{})
	conn := &compressingConn{Conn: trametest.NewConn(), sizes: make(chan int, 8)}
	go func() { _ = h.Serve(context.Background(), conn, "a.txt", Peer{ID: "2"}) }()
	t.Cleanup(func() { conn.Close() })
	<-conn.Out
	conn.In <- []byte(`{"t":"sync"}`)
	<-conn.Out
	select {
	case size := <-conn.sizes:
		if size < compressFrom {
			t.Fatalf("compressed a %d-byte frame", size)
		}
	default:
		t.Fatal("large frame not compressed")
	}
	if len(conn.sizes) != 0 {
		t.Fatal("small frame compressed")
	}
}

func TestSaveFailureIsReportedAndRetried(t *testing.T) {
	store := trametest.NewStore()
	h := NewHub(store, Text, fastOptions())
	a, _, _ := join(t, h, "a.txt", Peer{ID: "1"})

	store.Fail(errors.New("disk full"))
	a.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"a"}]}]}`)
	a.Expect("ack")
	<-store.Saves
	if f := a.Expect("error"); f.Error != "disk full" {
		t.Fatalf("error = %+v", f)
	}

	late := connect(t, h, context.Background(), "a.txt", Peer{ID: "2"})
	if f := late.Expect("hello"); f.Error != "disk full" {
		t.Fatalf("hello = %+v", f)
	}
	a.Expect("join")

	store.Fail(nil)
	<-store.Saves
	for _, c := range []*trametest.Client{a, late} {
		if f := c.Expect("saved"); f.V != 1 {
			t.Fatalf("saved = %+v", f)
		}
	}
}

func TestLastLeaveSavesAndUnloads(t *testing.T) {
	store := trametest.NewStore()
	h := NewHub(store, Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	a, _, _ := join(t, h, "a.txt", Peer{ID: "1"})
	a.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"a"}]}]}`)
	a.Expect("ack")
	a.Leave()

	if got := store.File("a.txt"); got != "a" {
		t.Fatalf("saved %q", got)
	}
	h.mu.Lock()
	loaded := len(h.rooms)
	h.mu.Unlock()
	if loaded != 0 {
		t.Fatalf("%d documents still loaded", loaded)
	}
}

func TestLoadFailureClosesConnection(t *testing.T) {
	store := trametest.NewStore()
	store.Fail(errors.New("file not found"))
	h := NewHub(store, Text, Options{})
	c := connect(t, h, context.Background(), "a.txt", Peer{ID: "1"})
	if err := <-c.Done; err == nil || err.Error() != "file not found" {
		t.Fatalf("Serve = %v", err)
	}
	if code, reason := c.Conn.CloseFrame(); code != CloseLoadFailed || reason != "file not found" {
		t.Fatalf("close %d %q", code, reason)
	}

	store.Fail(nil)
	again := connect(t, h, context.Background(), "a.txt", Peer{ID: "1"})
	again.Expect("hello")
}

func TestCancelledContextRevokes(t *testing.T) {
	h := NewHub(trametest.NewStore(), Text, Options{})
	ctx, cancel := context.WithCancelCause(context.Background())
	c := connect(t, h, ctx, "a.txt", Peer{ID: "1"})
	c.Expect("hello")
	cancel(errors.New("share removed"))
	select {
	case <-c.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
	if code, reason := c.Conn.CloseFrame(); code != CloseRevoked || reason != "share removed" {
		t.Fatalf("close %d %q", code, reason)
	}
}

func TestCloseSavesEverything(t *testing.T) {
	store := trametest.NewStore()
	h := NewHub(store, Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	a, _, _ := join(t, h, "a.txt", Peer{ID: "1"})
	a.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"a"}]}]}`)
	a.Expect("ack")

	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.File("a.txt"); got != "a" {
		t.Fatalf("saved %q", got)
	}
	if code, _ := a.Conn.CloseFrame(); code != CloseShutdown {
		t.Fatalf("close code %d", code)
	}
	late := connect(t, h, context.Background(), "a.txt", Peer{ID: "2"})
	if err := <-late.Done; !errors.Is(err, ErrClosed) {
		t.Fatalf("Serve after Close = %v", err)
	}
}

func TestClosePayloadTruncatesOnRuneBoundary(t *testing.T) {
	p := closePayload(CloseRevoked, strings.Repeat("é", 100))
	if len(p) > 125 || !strings.HasPrefix(string(p[2:]), "é") || !utf8.ValidString(string(p[2:])) {
		t.Fatalf("payload %q", p)
	}
}

func TestGoneDocumentDisconnectsAndUnloads(t *testing.T) {
	store := trametest.NewStore()
	h := NewHub(store, Text, fastOptions())
	a, _, _ := join(t, h, "a.txt", Peer{ID: "1"})

	store.Fail(fmt.Errorf("file deleted: %w", ErrGone))
	a.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"a"}]}]}`)
	a.Expect("ack")
	select {
	case <-a.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
	if code, reason := a.Conn.CloseFrame(); code != CloseRevoked || !strings.HasPrefix(reason, "file deleted") {
		t.Fatalf("close %d %q", code, reason)
	}
	h.mu.Lock()
	loaded := len(h.rooms)
	h.mu.Unlock()
	if loaded != 0 {
		t.Fatalf("%d documents still loaded", loaded)
	}
}

// followed is a text file that follows each edit with its author's id, put
// at the end of the text.
type followed struct{ File }

func (f followed) Follow(doc *ot.Tree, e ot.Edit, _ []ot.Edit, by Peer) ot.Edit {
	n := doc.Node(TextBody).Text.Len()
	more := ot.Edit{{Op: ot.OpTxt, ID: TextBody, Text: ot.Delta{{Retain: n - 1}, {Insert: by.ID}}}}
	if doc.Apply(more) != nil {
		return nil
	}
	return more
}

func TestFollowerKnowsTheAuthor(t *testing.T) {
	store := trametest.NewStore()
	h := NewHub(store, func(key string, data []byte) (*ot.Tree, File, error) {
		doc, f, err := Text(key, data)
		return doc, followed{f}, err
	}, fastOptions())
	a, _, _ := join(t, h, "a.txt", Peer{ID: "alice"})
	a.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"x"}]}]}`)
	a.Expect("ack")
	if f := a.Expect("op"); f.SID != 0 || string(f.D) != `[{"o":"txt","id":"body","x":[{"r":1},{"i":"alice"}]}]` {
		t.Fatalf("op = %+v", f)
	}
	<-store.Saves
	if got := store.File("a.txt"); got != "xalice" {
		t.Fatalf("saved %q", got)
	}
}

// stamped is a text file that keeps beside it how many times it was saved.
type stamped struct {
	File
	read  []byte
	saves int
}

func (s *stamped) ReadMeta(_ *ot.Tree, meta []byte) error {
	s.read = meta
	return nil
}

func (s *stamped) EncodeMeta(*ot.Tree) ([]byte, error) {
	s.saves++
	return []byte(strconv.Itoa(s.saves)), nil
}

func TestMetaIsReadAndSavedBesideTheFile(t *testing.T) {
	store := trametest.NewStore()
	store.Data["a.txt"] = []byte("one")
	store.Meta["a.txt"] = []byte("kept")
	var file *stamped
	h := NewHub(store, func(key string, data []byte) (*ot.Tree, File, error) {
		doc, f, err := Text(key, data)
		file = &stamped{File: f}
		return doc, file, err
	}, fastOptions())

	alice, _, _ := join(t, h, "a.txt", Peer{ID: "1", Name: "Alice", Client: "ca"})
	if string(file.read) != "kept" {
		t.Fatalf("meta read %q", file.read)
	}
	alice.Send(`{"t":"op","n":1,"v":0,"d":[{"o":"txt","id":"body","x":[{"i":"X"}]}]}`)
	alice.Expect("ack")
	<-store.Saves
	alice.Expect("saved")
	if got := string(store.Meta["a.txt"]); got != "1" {
		t.Fatalf("meta saved %q", got)
	}
}
