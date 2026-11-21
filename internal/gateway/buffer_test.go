package gateway

import (
	"fmt"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sergezossimov0/meters/internal/domain"
)

// rdg is a reading of device s at hour h with value v thousandths.
func rdg(s string, h int, v int64) Reading {
	return Reading{Serial: s, Model: "M-" + s, MeasuredAt: t0.Add(time.Duration(h) * time.Hour), Value: domain.Value(v)}
}

// flat lists the readings of a request as "serial@hour=value".
func flat(req domain.BatchRequest) []string {
	var out []string
	for _, it := range req.Items {
		for _, r := range it.Readings {
			out = append(out, fmt.Sprintf("%s@%d=%s", it.DeviceSerial, int(r.MeasuredAt.Sub(t0).Hours()), r.Value))
		}
	}
	return out
}

func TestBuffer_TakeGroupsByDevice(t *testing.T) {
	b := NewBuffer(nil)
	b.Add(rdg("A", 1, 1000), rdg("B", 1, 5), rdg("A", 2, 1500))
	batch, ok := b.Take(10)
	if !ok || batch.Size != 3 {
		t.Fatalf("Take = %+v, %v", batch, ok)
	}
	req := batch.Request
	if req.BatchID != batch.ID || !regexp.MustCompile(`^[0-9a-f]{16}-\d{6}$`).MatchString(batch.ID) {
		t.Errorf("batch_id %q", batch.ID)
	}
	if len(req.Items) != 2 || req.Items[0].DeviceSerial != "A" || req.Items[0].Model != "M-A" || req.Items[1].DeviceSerial != "B" {
		t.Fatalf("items = %+v", req.Items)
	}
	if got := flat(req); !slices.Equal(got, []string{"A@1=1.000", "A@2=1.500", "B@1=0.005"}) {
		t.Errorf("readings = %v", got)
	}
	// The request passes the server's checks.
	if _, rej, err := domain.ValidateBatch(req, t0.Add(3*time.Hour)); err != nil || len(rej) != 0 {
		t.Errorf("server would refuse it: %v %v", err, rej)
	}
}

func TestBuffer_InFlightIsNotTakenTwice(t *testing.T) {
	b := NewBuffer(nil)
	b.Add(rdg("A", 1, 1), rdg("A", 2, 2), rdg("A", 3, 3), rdg("A", 4, 4), rdg("A", 5, 5))

	first, _ := b.Take(2)
	second, _ := b.Take(2)
	third, _ := b.Take(2)
	if _, ok := b.Take(2); ok {
		t.Fatal("everything is in flight, Take must report nothing")
	}
	if first.ID == second.ID || second.ID == third.ID {
		t.Error("batch IDs repeat")
	}
	if got := [][]string{flat(first.Request), flat(second.Request), flat(third.Request)}; !slices.Equal(got[0], []string{"A@1=0.001", "A@2=0.002"}) ||
		!slices.Equal(got[1], []string{"A@3=0.003", "A@4=0.004"}) || !slices.Equal(got[2], []string{"A@5=0.005"}) {
		t.Errorf("batches = %v", got)
	}
	if b.Len() != 5 {
		t.Errorf("Len = %d: in-flight readings stay in the buffer", b.Len())
	}

	// Release returns the readings, oldest first again, under a new ID.
	if n := b.Release(second.ID); n != 2 {
		t.Errorf("Release = %d", n)
	}
	b.Add(rdg("A", 6, 6))
	again, _ := b.Take(10)
	if again.ID == second.ID || !slices.Equal(flat(again.Request), []string{"A@3=0.003", "A@4=0.004", "A@6=0.006"}) {
		t.Errorf("after release: %s %v", again.ID, flat(again.Request))
	}

	// Ack removes only its own batch.
	if n := b.Ack(first.ID); n != 2 || b.Len() != 4 {
		t.Errorf("Ack = %d, Len = %d", n, b.Len())
	}
	if n := b.Ack(first.ID); n != 0 {
		t.Errorf("second Ack = %d", n)
	}
	if n := b.Release("unknown"); n != 0 {
		t.Errorf("Release(unknown) = %d", n)
	}
	b.Ack(third.ID)
	b.Ack(again.ID)
	if b.Len() != 0 {
		t.Errorf("Len = %d after all acks", b.Len())
	}
}

func TestBuffer_RestoredAreFreeAndIDsDiffer(t *testing.T) {
	old := NewBuffer(nil)
	old.Add(rdg("A", 1, 1), rdg("A", 2, 2))
	inFlight, _ := old.Take(1)
	saved, _ := old.Snapshot()
	if len(saved) != 2 {
		t.Fatalf("snapshot = %v: in-flight readings must be saved", saved)
	}

	restored := NewBuffer(saved)
	batch, ok := restored.Take(10)
	if !ok || batch.Size != 2 {
		t.Errorf("restored Take = %+v: in-flight readings are free after a restart", batch)
	}
	if batch.ID[:16] == inFlight.ID[:16] {
		t.Error("a new buffer must use a new batch_id prefix")
	}
}

func TestBuffer_Version(t *testing.T) {
	b := NewBuffer(nil)
	_, v0 := b.Snapshot()
	b.Add()
	if _, v := b.Snapshot(); v != v0 {
		t.Error("adding nothing is not a change")
	}
	b.Add(rdg("A", 1, 1))
	_, v1 := b.Snapshot()
	batch, _ := b.Take(1)
	_, v2 := b.Snapshot()
	b.Release(batch.ID)
	_, v3 := b.Snapshot()
	b.Release(batch.ID)
	_, v4 := b.Snapshot()
	if v0 >= v1 || v1 >= v2 || v2 >= v3 || v3 != v4 {
		t.Errorf("versions %d %d %d %d %d", v0, v1, v2, v3, v4)
	}
}

// Producers, senders and the saver work at once: every reading ends up in
// exactly one acknowledged batch.
func TestBuffer_Concurrent(t *testing.T) {
	b := NewBuffer(nil)
	const producers, perProducer, senders = 4, 500, 4
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[string]int{}
	)
	for p := range producers {
		wg.Go(func() {
			for i := range perProducer {
				b.Add(rdg(fmt.Sprintf("P%d", p), i, int64(i)))
			}
		})
	}
	done := make(chan struct{})
	var sendWG sync.WaitGroup
	for range senders {
		sendWG.Go(func() {
			for retry := 0; ; retry++ {
				batch, ok := b.Take(37)
				if !ok {
					select {
					case <-done:
						if b.Len() == 0 {
							return
						}
					default:
					}
					continue
				}
				if retry%3 == 0 { // every third send "fails"
					b.Release(batch.ID)
					continue
				}
				mu.Lock()
				for _, r := range flat(batch.Request) {
					seen[r]++
				}
				mu.Unlock()
				b.Ack(batch.ID)
			}
		})
	}
	sendWG.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				b.Snapshot()
			}
		}
	})
	wg.Wait()
	close(done)
	sendWG.Wait()

	if len(seen) != producers*perProducer {
		t.Errorf("%d distinct readings acknowledged, want %d", len(seen), producers*perProducer)
	}
	for r, n := range seen {
		if n != 1 {
			t.Errorf("%s acknowledged %d times", r, n)
		}
	}
}
