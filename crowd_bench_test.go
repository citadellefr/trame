package trame

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/citadellefr/trame/trametest"
)

// tcpConn writes frames to a loopback socket, one write each as a WebSocket
// connection does, so that a benchmark pays what the kernel charges.
type tcpConn struct {
	*trametest.Conn
	c net.Conn
}

func (t tcpConn) WriteMessage(_ int, data []byte) error {
	_, err := t.c.Write(data)
	return err
}

func (t tcpConn) Close() error {
	t.c.Close()
	return t.Conn.Close()
}

func crowd(b *testing.B, n int, hello string) (*room, []*peer) {
	store := trametest.NewStore()
	store.Data["b.txt"] = []byte(strings.Repeat(strings.Repeat("lorem ipsum ", 40)+"\n", 2000))
	h := NewHub(store, Text, Options{SaveDelay: time.Hour, SaveMaxDelay: time.Hour})
	r, err := h.acquire(context.Background(), "b.txt")
	if err != nil {
		b.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, c)
		}
	}()
	var peers []*peer
	for i := range n {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			b.Fatal(err)
		}
		p := newPeer(tcpConn{trametest.NewConn(), c}, Peer{ID: strconv.Itoa(i), Client: "c" + strconv.Itoa(i)}, 1<<30)
		r.join(p)
		go p.writeLoop()
		r.handle(p, []byte(hello))
		peers = append(peers, p)
	}
	b.Cleanup(func() {
		for _, p := range peers {
			p.close(0, "")
		}
		l.Close()
		r.stop()
	})
	return r, peers
}

// BenchmarkCrowd measures the processor time a second of 128 people typing
// in the same document takes, each five characters a second in a paragraph
// of their own, on a revision 50 ms old, the cursor following: handed to
// them frame by frame, then in batches.
func BenchmarkCrowd(b *testing.B) {
	b.Run("frames", func(b *testing.B) { benchCrowd(b, `{"t":"sync"}`) })
	b.Run("batches", func(b *testing.B) { benchCrowd(b, `{"t":"sync","batch":true}`) })
}

func benchCrowd(b *testing.B, hello string) {
	const (
		n     = 128
		every = 200 * time.Millisecond
		lag   = 50 * time.Millisecond
	)
	r, peers := crowd(b, n, hello)
	var before, after syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &before)
	b.ResetTimer()
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			at := strconv.Itoa(i*481 + 10)
			eph := []byte(`{"t":"eph","d":{"s":{"id":"body","a":` + at + `,"f":` + at + `}}}`)
			time.Sleep(every * time.Duration(i) / n)
			for k := range b.N {
				r.mu.Lock()
				v := strconv.FormatUint(r.version, 10)
				r.mu.Unlock()
				time.Sleep(lag)
				change := `{"i":"a"}`
				if k%2 == 1 {
					change = `{"d":1}`
				}
				r.handle(p, []byte(`{"t":"op","n":`+strconv.Itoa(k+1)+`,"v":`+v+`,"d":[{"o":"txt","id":"body","x":[{"r":`+at+`},`+change+`]}]}`))
				r.handle(p, eph)
				time.Sleep(every - lag)
			}
		}()
	}
	wg.Wait()
	b.StopTimer()
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &after)
	used := time.Duration(after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano())
	b.ReportMetric(float64(used.Milliseconds())/b.Elapsed().Seconds(), "cpu-ms/s")
	for _, p := range peers {
		select {
		case <-p.done:
			b.Fatal("a peer was disconnected")
		default:
		}
	}
}
