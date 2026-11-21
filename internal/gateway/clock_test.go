package gateway

import (
	"testing"
	"testing/synctest"
	"time"
)

const day = 24 * time.Hour

func TestClock_RunsFastThenCatchesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		c := NewClock(30*day, 3600) // 1 real second = 1 model hour

		if got, want := c.Now(), start.Add(-30*day).UTC(); !got.Equal(want) {
			t.Fatalf("at start: %v, want %v", got, want)
		}
		if c.CaughtUp() {
			t.Fatal("caught up at start")
		}

		time.Sleep(10 * time.Second)
		if got, want := c.Now(), start.Add(-30*day+10*time.Hour).UTC(); !got.Equal(want) {
			t.Errorf("after 10 s: %v, want %v", got, want)
		}

		// The model gains 3599 s per second: 30 days take 720 h / 3599 ≈ 720.2 s.
		catchUp := 30 * day / 3599
		time.Sleep(catchUp - 10*time.Second - time.Second)
		if c.CaughtUp() || !c.Now().Before(time.Now()) {
			t.Errorf("a second before catching up: model %v, real %v", c.Now(), time.Now())
		}
		time.Sleep(time.Second)
		if !c.CaughtUp() || !c.Now().Equal(time.Now().UTC()) {
			t.Errorf("at catch-up: model %v, real %v, caught up %v", c.Now(), time.Now(), c.CaughtUp())
		}

		// From then on it is real time: speed 1, never ahead.
		for range 5 {
			time.Sleep(time.Hour)
			if now := time.Now().UTC(); !c.Now().Equal(now) {
				t.Errorf("after catching up: model %v, real %v", c.Now(), now)
			}
		}
	})
}

// The model never runs ahead of real time, at any moment of the catch-up.
func TestClock_NeverAhead(t *testing.T) {
	for _, tt := range []struct {
		backfill time.Duration
		speed    int
	}{{30 * day, 3600}, {7 * time.Hour, 7}, {time.Hour, 2}, {90 * day, 1_000_000}} {
		synctest.Test(t, func(t *testing.T) {
			c := NewClock(tt.backfill, tt.speed)
			for range 2000 {
				time.Sleep(317 * time.Millisecond)
				if m, now := c.Now(), time.Now(); m.After(now) {
					t.Fatalf("backfill %v, speed %d: model %v ahead of %v", tt.backfill, tt.speed, m, now)
				}
			}
		})
	}
}

func TestClock_NoBackfill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClock(0, 3600)
		if !c.CaughtUp() || !c.Now().Equal(time.Now().UTC()) {
			t.Errorf("backfill 0: model %v, caught up %v", c.Now(), c.CaughtUp())
		}
		time.Sleep(time.Minute)
		if !c.Now().Equal(time.Now().UTC()) {
			t.Error("backfill 0 must be real time")
		}
	})
}

// With speed 1 the model keeps its lag: it runs at real speed, behind.
func TestClock_SpeedOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := NewClock(time.Hour, 1)
		time.Sleep(10 * time.Hour)
		if got, want := c.Now(), time.Now().Add(-time.Hour).UTC(); !got.Equal(want) || c.CaughtUp() {
			t.Errorf("model %v, want %v, caught up %v", got, want, c.CaughtUp())
		}
		if c := NewClock(time.Hour, 0); c.speed != 1 {
			t.Errorf("speed 0 is taken as %d, want 1", c.speed)
		}
	})
}

func TestResumeClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		saved := time.Now().Add(-2 * day)
		c := ResumeClock(saved, 3600)
		if !c.Now().Equal(saved.UTC()) {
			t.Fatalf("resumed at %v, want %v", c.Now(), saved)
		}
		time.Sleep(24 * time.Second)
		if got, want := c.Now(), saved.Add(day).UTC(); !got.Equal(want) {
			t.Errorf("after 24 s: %v, want %v", got, want)
		}

		// A saved time in the future (the clock moved back) starts at now.
		ahead := ResumeClock(time.Now().Add(time.Hour), 3600)
		if !ahead.Now().Equal(time.Now().UTC()) || !ahead.CaughtUp() {
			t.Errorf("future saved time: model %v, real %v", ahead.Now(), time.Now())
		}
	})
}

func TestClock_NowIsUTCWithoutMonotonic(t *testing.T) {
	c := NewClock(day, 3600)
	m := c.Now()
	if m.Location() != time.UTC || m != m.Round(0) {
		t.Errorf("Now() = %#v: want UTC without a monotonic reading", m)
	}
}
