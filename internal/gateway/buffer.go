package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"

	"github.com/sergezossimov0/meters/internal/domain"
)

// Buffer keeps readings until the server has them (spec п. 7.2). A reading
// taken into a batch is "in flight": it belongs to that batch_id and is not
// offered to other batches until the send ends with Ack (the server has it,
// or it is poison) or Release (try again later, under a new batch_id).
// Safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	entries []entry // arrival order: oldest first
	prefix  string  // random per start, so batch IDs never repeat
	seq     uint64
	version uint64 // bumped by every change; the file saver compares it
}

type entry struct {
	Reading
	batchID string // "" — free
}

// Batch is a set of readings taken from the buffer under one batch_id.
type Batch struct {
	ID      string
	Request domain.BatchRequest
	Size    int // number of readings
}

// NewBuffer returns a buffer holding restored readings, all free.
func NewBuffer(restored []Reading) *Buffer {
	var p [8]byte
	_, _ = rand.Read(p[:]) // never fails since Go 1.24
	b := &Buffer{prefix: hex.EncodeToString(p[:])}
	for _, r := range restored {
		b.entries = append(b.entries, entry{Reading: r})
	}
	return b
}

// Add appends readings.
func (b *Buffer) Add(rs ...Reading) {
	if len(rs) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range rs {
		b.entries = append(b.entries, entry{Reading: r})
	}
	b.version++
}

// Take puts up to max free readings, oldest first, into a new batch. ok is
// false when there is nothing free.
func (b *Buffer) Take(max int) (batch Batch, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var picked []int
	for i := range b.entries {
		if len(picked) == max {
			break
		}
		if b.entries[i].batchID == "" {
			picked = append(picked, i)
		}
	}
	if len(picked) == 0 {
		return Batch{}, false
	}
	b.seq++
	id := fmt.Sprintf("%s-%06d", b.prefix, b.seq)

	// One item per device, in the order the devices first appear; the
	// readings of a device are in arrival order, which is time order.
	req := domain.BatchRequest{BatchID: id}
	pos := map[string]int{}
	for _, i := range picked {
		e := &b.entries[i]
		e.batchID = id
		p, seen := pos[e.Serial]
		if !seen {
			p = len(req.Items)
			pos[e.Serial] = p
			req.Items = append(req.Items, domain.ItemRequest{DeviceSerial: e.Serial, Model: e.Model})
		}
		req.Items[p].Readings = append(req.Items[p].Readings,
			domain.ReadingRequest{MeasuredAt: e.MeasuredAt, Value: e.Value.String()})
	}
	b.version++
	return Batch{ID: id, Request: req, Size: len(picked)}, true
}

// Ack removes the readings of the batch: the server has stored or refused
// them for good. It returns how many were removed.
func (b *Buffer) Ack(batchID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.entries)
	b.entries = slices.DeleteFunc(b.entries, func(e entry) bool { return e.batchID == batchID })
	if removed := n - len(b.entries); removed > 0 {
		b.version++
		return removed
	}
	return 0
}

// Release makes the readings of the batch free again. It returns how many.
func (b *Buffer) Release(batchID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for i := range b.entries {
		if b.entries[i].batchID == batchID {
			b.entries[i].batchID = ""
			n++
		}
	}
	if n > 0 {
		b.version++
	}
	return n
}

// Len returns the number of readings, in flight included.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// Snapshot returns all readings, in flight included (spec п. 7.3), and the
// version of the buffer they belong to.
func (b *Buffer) Snapshot() ([]Reading, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Reading, len(b.entries))
	for i, e := range b.entries {
		out[i] = e.Reading
	}
	return out, b.version
}
