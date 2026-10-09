package trame

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/citadellefr/trame/ot"
)

const (
	textMessage  = 1
	closeMessage = 8
	pingMessage  = 9

	maxPresenceBytes  = 16 << 10
	closeReasonLength = 123
)

// inbound is any frame a client sends:
//
//	{"t":"sync","epoch":"…","v":41,"batch":true}
//	{"t":"op","n":7,"v":41,"d":[{"o":"txt","id":"body","x":[{"r":3},{"i":"a"}]}]}
//	{"t":"eph","d":{...}}
//
// A client sends sync first, with the revision it last saw, if any, and
// whether it reads several frames sent as one message, a JSON array of them.
// It sends an edit with the revision it made it on.
type inbound struct {
	T     string          `json:"t"`
	N     uint64          `json:"n"`
	V     uint64          `json:"v"`
	Epoch string          `json:"epoch"`
	Batch bool            `json:"batch"`
	D     json.RawMessage `json:"d"`

	// edit is D read as an edit, bad when it is not one: done before the
	// room is locked, which it does not need. exact tells whether D is as
	// compact as the edit can be written.
	edit  ot.Edit
	bad   bool
	exact bool
	read  bool
}

func (in *inbound) decodeEdit() {
	var err error
	in.edit, in.exact, err = ot.DecodeEdit(in.D)
	in.bad, in.read = err != nil || in.edit.Check() != nil, true
	if in.bad {
		in.edit, in.exact = nil, false
	}
}

var opPrefix = []byte(`{"t":"op","n":`)

// readOp reads an edit frame written the way the Flutter client writes it,
// without decoding the envelope. When the frame is anything else, or its
// edit is not one, it is left to json.Unmarshal.
func (in *inbound) readOp(msg []byte) bool {
	rest, ok := bytes.CutPrefix(msg, opPrefix)
	if !ok {
		return false
	}
	n, rest, ok := cutUint(rest, `,"v":`)
	if !ok {
		return false
	}
	v, rest, ok := cutUint(rest, `,"d":`)
	if !ok {
		return false
	}
	d, ok := bytes.CutSuffix(rest, []byte{'}'})
	if !ok {
		return false
	}
	*in = inbound{T: "op", N: n, V: v, D: d}
	in.decodeEdit()
	return !in.bad
}

// cutUint reads digits, then the separator that must follow them.
func cutUint(b []byte, sep string) (n uint64, rest []byte, ok bool) {
	i := 0
	for i < len(b) && '0' <= b[i] && b[i] <= '9' {
		n = n*10 + uint64(b[i]-'0')
		i++
	}
	if i == 0 || i > 18 || i > 1 && b[0] == '0' {
		return 0, nil, false
	}
	rest, ok = bytes.CutPrefix(b[i:], []byte(sep))
	return n, rest, ok
}

var presencePrefix = []byte(`{"t":"eph","d":`)

// presenceData reads a presence frame written the way the Flutter client
// writes it with a single scan instead of a full decode: presence is most of
// the traffic. Any other frame is left to json.Unmarshal.
func presenceData(msg []byte) ([]byte, bool) {
	d, ok := bytes.CutPrefix(msg, presencePrefix)
	if !ok {
		return nil, false
	}
	d, ok = bytes.CutSuffix(d, []byte{'}'})
	return d, ok && json.Valid(d)
}

func presenceFrame(sid uint32, d []byte) []byte {
	b := make([]byte, 0, len(d)+32)
	b = append(b, `{"t":"eph","sid":`...)
	b = strconv.AppendUint(b, uint64(sid), 10)
	b = append(b, `,"d":`...)
	b = append(b, d...)
	return append(b, '}')
}

// opFrame hands out an edit, as the hub applied it, with the revision it
// made.
func opFrame(sid uint32, version uint64, edit []byte) []byte {
	b := make([]byte, 0, len(edit)+48)
	b = append(b, `{"t":"op","sid":`...)
	b = strconv.AppendUint(b, uint64(sid), 10)
	b = append(b, `,"v":`...)
	b = strconv.AppendUint(b, version, 10)
	b = append(b, `,"d":`...)
	b = append(b, edit...)
	return append(b, '}')
}

func ackFrame(n, version uint64) []byte {
	b := make([]byte, 0, 48)
	b = append(b, `{"t":"ack","n":`...)
	b = strconv.AppendUint(b, n, 10)
	b = append(b, `,"v":`...)
	b = strconv.AppendUint(b, version, 10)
	return append(b, '}')
}

// readyFrame ends the edits a reconnecting client missed.
func readyFrame(version uint64) []byte {
	b := append([]byte(`{"t":"ready","v":`), strconv.FormatUint(version, 10)...)
	return append(b, '}')
}

// docFrame is the whole document, for a client that cannot catch up edit by
// edit, with the last edit of its client the hub applied.
func docFrame(version, ack uint64, nodes []byte) []byte {
	b := make([]byte, 0, len(nodes)+64)
	b = append(b, `{"t":"doc","v":`...)
	b = strconv.AppendUint(b, version, 10)
	b = append(b, `,"ack":`...)
	b = strconv.AppendUint(b, ack, 10)
	b = append(b, `,"d":`...)
	b = append(b, nodes...)
	return append(b, '}')
}

func savedFrame(version uint64) []byte {
	b := append([]byte(`{"t":"saved","v":`), strconv.FormatUint(version, 10)...)
	return append(b, '}')
}

func messageFrame(t string, n uint64, message string) []byte {
	f, _ := json.Marshal(struct {
		T     string `json:"t"`
		N     uint64 `json:"n,omitempty"`
		Error string `json:"error"`
	}{t, n, message})
	return f
}

type peerView struct {
	SID      uint32 `json:"sid"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	ReadOnly bool   `json:"ro,omitempty"`
}

func joinFrame(p peerView) []byte {
	f, _ := json.Marshal(struct {
		T    string   `json:"t"`
		Peer peerView `json:"peer"`
	}{"join", p})
	return f
}

func leaveFrame(sid uint32) []byte {
	b := append([]byte(`{"t":"leave","sid":`), strconv.FormatUint(uint64(sid), 10)...)
	return append(b, '}')
}

// hello is the first frame of a connection: who it is, who else is there,
// and which document it reached. The document itself follows sync.
type hello struct {
	T        string     `json:"t"`
	SID      uint32     `json:"sid"`
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Epoch    string     `json:"epoch"`
	Version  uint64     `json:"v"`
	Saved    uint64     `json:"saved"`
	ReadOnly bool       `json:"ro,omitempty"`
	Error    string     `json:"error,omitempty"`
	Peers    []peerView `json:"peers"`
}

func closePayload(code int, reason string) []byte {
	if len(reason) > closeReasonLength {
		reason = truncateUTF8(reason, closeReasonLength)
	}
	return append([]byte{byte(code >> 8), byte(code)}, reason...)
}

func truncateUTF8(s string, n int) string {
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
