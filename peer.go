package trame

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	outboxSize   = 512
	writeWait    = 10 * time.Second
	pingInterval = 25 * time.Second
	pongWait     = 60 * time.Second

	// compressFrom is the smallest frame worth compressing, when the
	// connection negotiated it: the document sent on connection, long pastes.
	compressFrom = 4 << 10

	// writeEvery spaces the writes to a client that reads batches: what is
	// queued meanwhile leaves as one message, of batchBytes or little more.
	writeEvery = 20 * time.Millisecond
	batchBytes = 64 << 10
)

// compressor is implemented by the Conn of gorilla/websocket and
// fasthttp/websocket.
type compressor interface {
	EnableWriteCompression(enable bool)
}

type peer struct {
	conn Conn
	info Peer
	sid  uint32
	// synced is set, under the room's lock, once the peer has the document:
	// edits are sent to it from then on.
	synced bool
	// batch tells that the client reads several frames in one message.
	batch atomic.Bool

	out      chan []byte
	done     chan struct{}
	finished chan struct{}
	once     sync.Once
	code     int
	reason   string

	rate   float64
	tokens float64
	refill time.Time
}

func newPeer(conn Conn, info Peer, rate int) *peer {
	return &peer{
		conn:     conn,
		info:     info,
		out:      make(chan []byte, outboxSize),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
		rate:     float64(rate),
		tokens:   float64(rate),
		refill:   time.Now(),
	}
}

func (p *peer) view() peerView {
	return peerView{SID: p.sid, ID: p.info.ID, Name: p.info.Name, ReadOnly: p.info.ReadOnly}
}

// send never blocks: a peer that cannot keep up is disconnected, and catches
// up when it reconnects.
func (p *peer) send(frame []byte) {
	select {
	case p.out <- frame:
	default:
		p.close(CloseTooSlow, "connection too slow")
	}
}

// close ends the connection, with a close frame when code is not zero.
func (p *peer) close(code int, reason string) {
	p.once.Do(func() {
		p.code, p.reason = code, reason
		close(p.done)
	})
}

func (p *peer) writeLoop() {
	defer close(p.finished)
	defer p.conn.Close()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	compress, _ := p.conn.(compressor)
	// out is nil while a batch fills: the first frame after a quiet time
	// leaves at once, the next ones together when pace fires.
	out := p.out
	pace := time.NewTimer(writeEvery)
	defer pace.Stop()
	for {
		select {
		case frame := <-out:
			deflate := len(frame) >= compressFrom
			if p.batch.Load() {
				frame, deflate = p.gather(frame)
				out = nil
				pace.Reset(writeEvery)
			}
			if !p.write(compress, frame, deflate) {
				p.close(0, "")
				return
			}
		case <-pace.C:
			out = p.out
		case <-ping.C:
			if p.conn.WriteControl(pingMessage, nil, time.Now().Add(writeWait)) != nil {
				p.close(0, "")
				return
			}
		case <-p.done:
			if p.code != 0 && p.code != CloseTooSlow {
				p.drain(compress)
			}
			if p.code != 0 {
				_ = p.conn.WriteControl(closeMessage, closePayload(p.code, p.reason), time.Now().Add(time.Second))
			}
			return
		}
	}
}

// gather joins frame and those queued behind it into one message, a JSON
// array of them, and tells whether it is worth compressing: a long frame is,
// a batch of keystrokes and cursors is not, however many they are.
func (p *peer) gather(frame []byte) ([]byte, bool) {
	deflate := len(frame) >= compressFrom
	if deflate || len(p.out) == 0 {
		return frame, deflate
	}
	b := make([]byte, 0, min(batchBytes, 2*len(frame)*(len(p.out)+1)))
	b = append(append(b, '['), frame...)
	for more := true; more && len(b) < batchBytes; {
		select {
		case next := <-p.out:
			b = append(append(b, ','), next...)
			deflate = deflate || len(next) >= compressFrom
		default:
			more = false
		}
	}
	return append(b, ']'), deflate
}

func (p *peer) write(compress compressor, frame []byte, deflate bool) bool {
	if compress != nil {
		compress.EnableWriteCompression(deflate)
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return p.conn.WriteMessage(textMessage, frame) == nil
}

// drain writes what is queued before a close frame: the acknowledgement of
// a last edit tells its client not to send it again.
func (p *peer) drain(compress compressor) {
	for {
		select {
		case frame := <-p.out:
			if !p.write(compress, frame, len(frame) >= compressFrom) {
				return
			}
		default:
			return
		}
	}
}

func (p *peer) readLoop(limit int64, handle func([]byte)) {
	p.conn.SetReadLimit(limit)
	_ = p.conn.SetReadDeadline(time.Now().Add(pongWait))
	p.conn.SetPongHandler(func(string) error {
		return p.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		kind, msg, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		if kind == textMessage {
			handle(msg)
		}
	}
}

// allowPresence is a token bucket: only the read loop calls it.
func (p *peer) allowPresence(now time.Time) bool {
	p.tokens = min(p.rate, p.tokens+now.Sub(p.refill).Seconds()*p.rate)
	p.refill = now
	if p.tokens < 1 {
		return false
	}
	p.tokens--
	return true
}
