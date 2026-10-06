package trame

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/citadellefr/trame/ot"
)

var (
	errReadOnly  = errors.New("read-only access")
	errMalformed = errors.New("malformed operation")
	errStale     = errors.New("revision unknown or too old")
	errTooLong   = errors.New("document size limit reached")
)

type room struct {
	hub   *Hub
	key   string
	ready chan struct{}
	err   error
	refs  int

	mu   sync.Mutex
	doc  *ot.Tree
	file File
	// epoch names this stay in memory: revisions count from its start.
	epoch   string
	history []edit
	peers   map[uint32]*peer
	nextSID uint32
	acks    map[string]uint64
	version uint64
	saved   uint64
	saveErr string

	saveMu   sync.Mutex
	meta     []byte // what the store keeps beside the file, as of the last load or save
	kick     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// edit is an applied edit, kept to rebase late edits over it and to replay
// it to a client that missed it.
type edit struct {
	raw    []byte
	ops    ot.Edit
	sid    uint32
	client string
	n      uint64
}

func newRoom(h *Hub, key string) *room {
	return &room{
		hub:   h,
		key:   key,
		ready: make(chan struct{}),
		epoch: rand.Text()[:12],
		peers: map[uint32]*peer{},
		acks:  map[string]uint64{},
		kick:  make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
}

func (r *room) load() {
	defer close(r.ready)
	data, err := r.hub.store.Load(context.Background(), r.key)
	if err == nil {
		r.doc, r.file, err = r.hub.format(r.key, data)
	}
	if err == nil {
		err = r.loadMeta()
	}
	if err != nil {
		r.err = err
		r.hub.forget(r)
		return
	}
	go r.saveLoop()
}

// loadMeta hands the format what the store keeps beside the file, when both
// have any.
func (r *room) loadMeta() error {
	f, ok := r.file.(MetaFile)
	if !ok {
		return nil
	}
	ms, ok := r.hub.store.(MetaStore)
	if !ok {
		return nil
	}
	meta, err := ms.LoadMeta(context.Background(), r.key)
	if err != nil {
		return err
	}
	if err := f.ReadMeta(r.doc, meta); err != nil {
		return err
	}
	r.meta = meta
	return nil
}

func (r *room) stop() {
	r.stopOnce.Do(func() { close(r.done) })
}

func (r *room) join(p *peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSID++
	p.sid = r.nextSID

	peers := make([]peerView, 0, len(r.peers))
	for _, q := range r.peers {
		peers = append(peers, q.view())
	}
	slices.SortFunc(peers, func(a, b peerView) int { return cmp.Compare(a.SID, b.SID) })
	f, _ := json.Marshal(hello{
		T:        "hello",
		SID:      p.sid,
		ID:       p.info.ID,
		Name:     p.info.Name,
		Epoch:    r.epoch,
		Version:  r.version,
		Saved:    r.saved,
		ReadOnly: p.info.ReadOnly,
		Error:    r.saveErr,
		Peers:    peers,
	})
	p.send(f)

	r.broadcast(joinFrame(p.view()), nil)
	r.peers[p.sid] = p
}

func (r *room) leave(p *peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, p.sid)
	r.broadcast(leaveFrame(p.sid), nil)
}

func (r *room) closePeers(code int, reason string) []*peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	peers := make([]*peer, 0, len(r.peers))
	for _, p := range r.peers {
		p.close(code, reason)
		peers = append(peers, p)
	}
	return peers
}

// broadcast sends to every peer but except. Callers hold r.mu.
func (r *room) broadcast(frame []byte, except *peer) {
	for _, p := range r.peers {
		if p != except {
			p.send(frame)
		}
	}
}

func (r *room) handle(p *peer, msg []byte) {
	if d, ok := presenceData(msg); ok {
		r.relayPresence(p, d)
		return
	}
	var in inbound
	if err := json.Unmarshal(msg, &in); err != nil {
		if in.T == "op" {
			p.send(messageFrame("nack", in.N, errMalformed.Error()))
		}
		return
	}
	switch in.T {
	case "sync":
		r.sync(p, &in)
	case "op":
		r.apply(p, &in)
	case "eph":
		r.relayPresence(p, in.D)
	}
}

func (r *room) relayPresence(p *peer, d []byte) {
	if len(d) == 0 || len(d) > maxPresenceBytes || !p.allowPresence(time.Now()) {
		return
	}
	r.mu.Lock()
	r.broadcast(presenceFrame(p.sid, d), p)
	r.mu.Unlock()
}

// sync brings a peer up to date: with the edits it missed when this is the
// document it last saw and they are still known, whole otherwise.
func (r *room) sync(p *peer, in *inbound) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.synced {
		return
	}
	p.synced = true
	first := r.version - uint64(len(r.history))
	if in.Epoch != r.epoch || in.V < first || in.V > r.version {
		nodes, _ := json.Marshal(r.doc.Edit())
		p.send(docFrame(r.version, r.acks[p.info.Client], nodes))
		return
	}
	for i, e := range r.history[in.V-first:] {
		v := in.V + uint64(i) + 1
		if e.client != "" && e.client == p.info.Client {
			p.send(ackFrame(e.n, v))
		} else {
			p.send(opFrame(e.sid, v, e.raw))
		}
	}
	p.send(readyFrame(r.version))
}

func (r *room) apply(p *peer, in *inbound) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !p.synced {
		return
	}
	client := p.info.Client
	if client != "" && in.N <= r.acks[client] {
		return
	}
	e, since, err := r.rebase(p, in)
	if err == nil && e.Growth() > 0 && r.doc.Len()+e.Growth() > r.hub.opt.MaxLength {
		err = errTooLong
	}
	if err == nil {
		err = r.doc.Apply(e)
	}
	if err != nil {
		p.send(messageFrame("nack", in.N, err.Error()))
		return
	}
	if e == nil {
		e = ot.Edit{}
	}
	if client != "" {
		r.acks[client] = in.N
	}
	r.record(e, p, client, in.N)
	p.send(ackFrame(in.N, r.version))
	if f, ok := r.file.(Follower); ok {
		if more := f.Follow(r.doc, e, since, p.info); len(more) > 0 {
			r.record(more, nil, "", 0)
		}
	}
	r.requestSave()
}

// record adds an applied edit to the history, as the next revision, and
// hands it to every peer but its author, which the server is when p is
// nil. Callers hold r.mu.
func (r *room) record(e ot.Edit, p *peer, client string, n uint64) {
	raw, _ := json.Marshal(e)
	var sid uint32
	if p != nil {
		sid = p.sid
	}
	r.history = append(r.history, edit{raw: raw, ops: e, sid: sid, client: client, n: n})
	if len(r.history) > r.hub.opt.History {
		r.history = slices.Clone(r.history[len(r.history)-r.hub.opt.History/2:])
	}
	r.version++
	frame := opFrame(sid, r.version, raw)
	for _, q := range r.peers {
		if q != p && q.synced {
			q.send(frame)
		}
	}
}

// rebase turns an edit made on an earlier revision into one on the current
// document, and returns the edits it was rebased over. Callers hold r.mu.
func (r *room) rebase(p *peer, in *inbound) (ot.Edit, []ot.Edit, error) {
	if p.info.ReadOnly {
		return nil, nil, errReadOnly
	}
	var e ot.Edit
	if json.Unmarshal(in.D, &e) != nil || e.Check() != nil {
		return nil, nil, errMalformed
	}
	if err := r.file.Check(r.doc, e, p.info); err != nil {
		return nil, nil, err
	}
	first := r.version - uint64(len(r.history))
	if in.V < first || in.V > r.version {
		return nil, nil, errStale
	}
	var since []ot.Edit
	for _, h := range r.history[in.V-first:] {
		e = ot.TransformEdit(h.ops, e, true)
		since = append(since, h.ops)
	}
	return e, since, nil
}

func (r *room) requestSave() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

func (r *room) dirty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version != r.saved
}

// saveLoop saves once edits pause for SaveDelay, or SaveMaxDelay after the
// first unsaved one, whichever comes first.
func (r *room) saveLoop() {
	opt := r.hub.opt
	for {
		select {
		case <-r.kick:
		case <-r.done:
			return
		}
		deadline := time.Now().Add(opt.SaveMaxDelay)
		timer := time.NewTimer(opt.SaveDelay)
	wait:
		for {
			select {
			case <-r.kick:
				timer.Reset(min(opt.SaveDelay, time.Until(deadline)))
			case <-timer.C:
				break wait
			case <-r.done:
				timer.Stop()
				return
			}
		}
		if err := r.flush(context.Background()); err != nil && !errors.Is(err, ErrGone) {
			time.AfterFunc(opt.SaveMaxDelay, r.requestSave)
			continue
		}
		r.hub.unloadIfIdle(r)
	}
}

// saveMeta writes what the format keeps beside the file, if it changed.
// Callers hold saveMu.
func (r *room) saveMeta(ctx context.Context, doc *ot.Tree) error {
	f, ok := r.file.(MetaFile)
	ms, hasStore := r.hub.store.(MetaStore)
	if !ok || !hasStore {
		return nil
	}
	meta, err := f.EncodeMeta(doc)
	if err != nil || bytes.Equal(meta, r.meta) {
		return err
	}
	if err := ms.SaveMeta(ctx, r.key, meta); err != nil {
		return err
	}
	r.meta = meta
	return nil
}

// flush saves the document if it changed since the last save, and tells
// every peer how it went.
func (r *room) flush(ctx context.Context) error {
	r.saveMu.Lock()
	defer r.saveMu.Unlock()

	r.mu.Lock()
	if r.version == r.saved {
		r.mu.Unlock()
		return nil
	}
	version := r.version
	doc := r.doc.Clone()
	r.mu.Unlock()

	data, err := r.file.Encode(doc)
	if err == nil {
		err = r.hub.store.Save(ctx, r.key, data)
	}
	if err == nil {
		err = r.saveMeta(ctx, doc)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if errors.Is(err, ErrGone) {
		r.saved = r.version
		for _, p := range r.peers {
			p.close(CloseRevoked, err.Error())
		}
		return err
	}
	if err != nil {
		r.saveErr = err.Error()
		r.broadcast(messageFrame("error", 0, r.saveErr), nil)
		return err
	}
	r.saved, r.saveErr = version, ""
	r.broadcast(savedFrame(version), nil)
	return nil
}
