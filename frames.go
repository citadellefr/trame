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
//	{"t":"sync","epoch":"…","v":41}
//	{"t":"op","n":7,"v":41,"d":[{"o":"txt","id":"body","x":[{"r":3},{"i":"a"}]}]}
//	{"t":"eph","d":{...}}
//
// A client sends sync first, with the revision it last saw, if any, and
// sends an edit with the revision it made it on.
type inbound struct {
	T     string          `json:"t"`
	N     uint64          `json:"n"`
	V     uint64          `json:"v"`
	Epoch string          `json:"epoch"`
	D     json.RawMessage `json:"d"`

	// edit is D read as an edit, bad when it is not one: done before the
	// room is locked, which it does not need.
	edit ot.Edit
	bad  bool
}

func (in *inbound) decodeEdit() {
	if json.Unmarshal(in.D, &in.edit) != nil || in.edit.Check() != nil {
		in.edit, in.bad = nil, true
	}
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
