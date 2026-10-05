// Package trametest is what tests of a trame hub, or of an application that
// serves one, connect with: a connection and a store in memory, and a client
// that reads the frames of the protocol.
package trametest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	textMessage  = 1
	closeMessage = 8
)

var errClosed = errors.New("trametest: connection closed")

// Conn is a WebSocket connection in memory: what the test sends arrives on
// In, what the hub writes leaves on Out.
type Conn struct {
	In  chan []byte
	Out chan []byte

	closed    chan struct{}
	closeOnce sync.Once

	mu          sync.Mutex
	closeCode   int
	closeReason string
}

func NewConn() *Conn {
	return &Conn{
		In:     make(chan []byte, 64),
		Out:    make(chan []byte, 1024),
		closed: make(chan struct{}),
	}
}

func (c *Conn) ReadMessage() (int, []byte, error) {
	select {
	case m := <-c.In:
		return textMessage, m, nil
	case <-c.closed:
		return 0, nil, errClosed
	}
}

func (c *Conn) WriteMessage(_ int, data []byte) error {
	select {
	case <-c.closed:
		return errClosed
	case c.Out <- data:
		return nil
	}
}

func (c *Conn) WriteControl(kind int, data []byte, _ time.Time) error {
	if kind == closeMessage && len(data) >= 2 {
		c.mu.Lock()
		c.closeCode, c.closeReason = int(data[0])<<8|int(data[1]), string(data[2:])
		c.mu.Unlock()
	}
	return nil
}

func (c *Conn) SetReadLimit(int64)                        {}
func (c *Conn) SetReadDeadline(time.Time) error           { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error          { return nil }
func (c *Conn) SetPongHandler(func(appData string) error) {}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

// CloseFrame is the code and reason of the close frame the hub sent, if any.
func (c *Conn) CloseFrame() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCode, c.closeReason
}

// Store keeps files in memory. A file never saved reads as empty. The key of
// every save, failed or not, goes to Saves.
type Store struct {
	Data  map[string][]byte
	Saves chan string

	mu   sync.Mutex
	fail error
}

func NewStore() *Store {
	return &Store{Data: map[string][]byte{}, Saves: make(chan string, 64)}
}

func (s *Store) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	return s.Data[key], nil
}

func (s *Store) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	err := s.fail
	if err == nil {
		s.Data[key] = data
	}
	s.mu.Unlock()
	s.Saves <- key
	return err
}

// Fail makes every load and save fail with err, until Fail(nil).
func (s *Store) Fail(err error) {
	s.mu.Lock()
	s.fail = err
	s.mu.Unlock()
}

func (s *Store) File(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.Data[key])
}

// Frame is any frame the hub sends.
type Frame struct {
	T     string          `json:"t"`
	SID   uint32          `json:"sid"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	N     uint64          `json:"n"`
	V     uint64          `json:"v"`
	Ack   uint64          `json:"ack"`
	Epoch string          `json:"epoch"`
	Saved uint64          `json:"saved"`
	Error string          `json:"error"`
	D     json.RawMessage `json:"d"`
	Peers []Peer          `json:"peers"`
	Peer  Peer            `json:"peer"`
}

// Peer is someone connected, as the hub describes them.
type Peer struct {
	SID      uint32 `json:"sid"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"ro"`
}

// Client is a connection a test drives frame by frame.
type Client struct {
	t    *testing.T
	Conn *Conn
	// Done receives what serve returned.
	Done chan error
}

// Connect runs serve on a new connection, typically a hub's Serve, until
// the test ends.
func Connect(t *testing.T, serve func(*Conn) error) *Client {
	t.Helper()
	c := &Client{t: t, Conn: NewConn(), Done: make(chan error, 1)}
	go func() { c.Done <- serve(c.Conn) }()
	t.Cleanup(func() { c.Conn.Close() })
	return c
}

// Join connects and syncs from scratch, returning the hello and doc frames.
func Join(t *testing.T, serve func(*Conn) error) (*Client, Frame, Frame) {
	t.Helper()
	c := Connect(t, serve)
	hello := c.Expect("hello")
	c.Send(`{"t":"sync"}`)
	return c, hello, c.Expect("doc")
}

func (c *Client) Send(s string) { c.Conn.In <- []byte(s) }

func (c *Client) Next() Frame {
	c.t.Helper()
	select {
	case raw := <-c.Conn.Out:
		var f Frame
		if err := json.Unmarshal(raw, &f); err != nil {
			c.t.Fatalf("invalid frame %s: %v", raw, err)
		}
		return f
	case <-time.After(2 * time.Second):
		c.t.Fatal("no frame")
		return Frame{}
	}
}

func (c *Client) Expect(kind string) Frame {
	c.t.Helper()
	f := c.Next()
	if f.T != kind {
		c.t.Fatalf("got %q frame, want %q: %+v", f.T, kind, f)
	}
	return f
}

// Quiet fails if a frame arrives soon.
func (c *Client) Quiet() {
	c.t.Helper()
	select {
	case raw := <-c.Conn.Out:
		c.t.Fatalf("unexpected frame %s", raw)
	case <-time.After(50 * time.Millisecond):
	}
}

// Leave closes the connection and waits for serve to return.
func (c *Client) Leave() {
	c.t.Helper()
	c.Conn.Close()
	select {
	case <-c.Done:
	case <-time.After(2 * time.Second):
		c.t.Fatal("serve did not return")
	}
}
