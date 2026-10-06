// Package trame is the server half of collaborative document editors. It
// keeps every open document in memory, orders the edits of the people
// connected to it, relays their cursors and saves the document through a
// Store.
//
// Edits are operational transforms (package ot): a client sends an edit
// with the revision it was made on, the hub rebases it over the edits
// received since, applies it and hands it to everyone with the next
// revision. Clients show their own edits at once and rebase them over what
// arrives, so nobody waits for the server and everybody converges.
//
// What a document is, how it is read from its file, which edits it takes and
// how it is written back is the Format the hub is given.
package trame

import (
	"context"
	"errors"
	"time"
)

// Conn is the part of a WebSocket connection the hub uses. The Conn types of
// github.com/gorilla/websocket and github.com/fasthttp/websocket satisfy it.
type Conn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetReadLimit(limit int64)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	Close() error
}

// Store reads and writes the files behind documents.
type Store interface {
	Load(ctx context.Context, key string) ([]byte, error)
	Save(ctx context.Context, key string, data []byte) error
}

// MetaStore is a Store that also keeps what a MetaFile holds beside its
// file. A file without any reads as nil.
type MetaStore interface {
	Store
	LoadMeta(ctx context.Context, key string) ([]byte, error)
	SaveMeta(ctx context.Context, key string, meta []byte) error
}

// Peer describes the person behind a connection.
type Peer struct {
	ID   string
	Name string
	// Client names the application instance across reconnections, so that
	// an edit it sends again after a drop is not applied twice.
	Client   string
	ReadOnly bool
}

// Options tunes a Hub. Zero fields take the defaults.
type Options struct {
	// SaveDelay is the quiet time after an edit before the document is saved.
	SaveDelay time.Duration
	// SaveMaxDelay bounds how long an edit stays unsaved while edits keep
	// coming, and spaces the retries of a failed save.
	SaveMaxDelay time.Duration
	// MaxLength bounds a document: its nodes and the UTF-16 code units of
	// their text.
	MaxLength       int
	MaxMessageBytes int64
	// History is how many past edits are kept to rebase late ones and to
	// catch up a client that reconnects.
	History int
	// PresenceRate is how many cursor frames a peer may send per second.
	PresenceRate int
}

// Close codes sent to a client whose connection the hub ends.
const (
	CloseLoadFailed = 4000
	CloseRevoked    = 4001
	CloseTooSlow    = 4002
	CloseShutdown   = 4003
)

var ErrClosed = errors.New("trame: hub closed")

// ErrGone is returned (possibly wrapped) by Store.Save when the file no
// longer exists, e.g. it was deleted: everyone connected to it is
// disconnected with the error as the reason, and its unsaved edits dropped.
var ErrGone = errors.New("trame: document no longer exists")

func (o Options) withDefaults() Options {
	if o.SaveDelay <= 0 {
		o.SaveDelay = 2 * time.Second
	}
	if o.SaveMaxDelay <= 0 {
		o.SaveMaxDelay = 10 * time.Second
	}
	if o.MaxLength <= 0 {
		o.MaxLength = 16 << 20
	}
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = 2 << 20
	}
	if o.History <= 0 {
		o.History = 1000
	}
	if o.PresenceRate <= 0 {
		o.PresenceRate = 40
	}
	return o
}
