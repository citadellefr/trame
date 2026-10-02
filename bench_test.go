package trame

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/citadellefr/trame/trametest"
)

// BenchmarkRelay measures what one keystroke costs the hub: decoding,
// rebasing it over an edit it missed, applying it to a document of 200
// pages and handing it to eight peers.
func BenchmarkRelay(b *testing.B) {
	store := trametest.NewStore()
	store.Data["b.txt"] = []byte(strings.Repeat(strings.Repeat("lorem ipsum ", 40)+"\n", 2000))
	h := NewHub(store, Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	r, err := h.acquire(context.Background(), "b.txt")
	if err != nil {
		b.Fatal(err)
	}
	defer r.stop()
	var peers []*peer
	for i := range 9 {
		p := newPeer(trametest.NewConn(), Peer{ID: strconv.Itoa(i), Client: "c" + strconv.Itoa(i)}, 40)
		r.join(p)
		r.handle(p, []byte(`{"t":"sync"}`))
		peers = append(peers, p)
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
	middle := strconv.Itoa(r.doc.Node(TextBody).Text.Len() / 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		v := strconv.FormatUint(r.version, 10)
		n := strconv.Itoa(i + 1)
		r.handle(peers[0], []byte(`{"t":"op","n":`+n+`,"v":`+v+`,"d":[{"o":"txt","id":"body","x":[{"r":`+middle+`},{"i":"a"}]}]}`))
	}
	b.StopTimer()
	for _, p := range peers {
		p.close(0, "")
	}
}
