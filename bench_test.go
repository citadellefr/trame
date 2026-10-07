package trame

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/citadellefr/trame/trametest"
)

// benchRoom is a room holding a document of 200 pages, joined by peers that
// read what they are sent and say nothing.
func benchRoom(b *testing.B, peers int) (*room, []*peer) {
	store := trametest.NewStore()
	store.Data["b.txt"] = []byte(strings.Repeat(strings.Repeat("lorem ipsum ", 40)+"\n", 2000))
	h := NewHub(store, Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	r, err := h.acquire(context.Background(), "b.txt")
	if err != nil {
		b.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() {
		for {
			select {
			case <-store.Saves:
			case <-stopped:
				return
			}
		}
	}()
	var out []*peer
	for i := range peers {
		p := newPeer(trametest.NewConn(), Peer{ID: strconv.Itoa(i), Client: "c" + strconv.Itoa(i)}, 40)
		r.join(p)
		r.handle(p, []byte(`{"t":"sync"}`))
		out = append(out, p)
		go func() {
			for {
				select {
				case <-p.out:
				case <-p.done:
					return
				}
			}
		}()
	}
	b.Cleanup(func() {
		close(stopped)
		for _, p := range out {
			p.close(0, "")
		}
		r.stop()
	})
	return r, out
}

// BenchmarkRelay measures what one keystroke costs the hub: decoding,
// rebasing it over an edit it missed, applying it to a document of 200
// pages and handing it to eight peers. A character is typed and deleted in
// turn, so that the paragraph keeps its length.
func BenchmarkRelay(b *testing.B) {
	r, peers := benchRoom(b, 9)
	middle := strconv.Itoa(r.doc.Node(TextBody).Text.Len() / 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		v := strconv.FormatUint(r.version, 10)
		n := strconv.Itoa(i + 1)
		change := `{"i":"a"}`
		if i%2 == 1 {
			change = `{"d":1}`
		}
		r.handle(peers[0], []byte(`{"t":"op","n":`+n+`,"v":`+v+`,"d":[{"o":"txt","id":"body","x":[{"r":`+middle+`},`+change+`]}]}`))
	}
}

// BenchmarkSync measures what a client connecting costs the hub: the whole
// document of 200 pages in one frame.
func BenchmarkSync(b *testing.B) {
	r, peers := benchRoom(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.mu.Lock()
		peers[0].synced = false
		r.mu.Unlock()
		r.handle(peers[0], []byte(`{"t":"sync"}`))
	}
}

// BenchmarkSave measures what a save costs: the document of 200 pages
// written as a file.
func BenchmarkSave(b *testing.B) {
	r, _ := benchRoom(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.mu.Lock()
		r.saved = r.version - 1
		r.mu.Unlock()
		if err := r.flush(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
