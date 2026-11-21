// Package gateway is the LPWAN gateway simulator (spec п. 7): it models
// meters in accelerated time, buffers their readings with a file on disk and
// sends them to the server through a pool of senders.
package gateway

import "time"

// Clock is the simulator's model time (spec п. 7.1). It starts in the past
// and runs speed times faster than real time until it catches up with
// time.Now; from then on it is real time. It never runs ahead of time.Now,
// so readings never become future_ts.
//
// Clock reads the ordinary time package: tests drive it with testing/synctest
// instead of a fake clock (spec п. 10.4).
type Clock struct {
	start     time.Time     // model time at realStart, wall clock only
	realStart time.Time     // with the monotonic reading
	catchUp   time.Duration // real time until the model reaches real time
	speed     time.Duration
}

// NewClock starts the model backfill before now. speed below 1 is taken as 1.
func NewClock(backfill time.Duration, speed int) *Clock {
	now := time.Now()
	return newClock(now.Round(0).Add(-max(backfill, 0)), now, speed)
}

// ResumeClock continues the model from a saved model time (-restore). A
// saved time ahead of now (the clock was moved back) starts at now.
func ResumeClock(model time.Time, speed int) *Clock {
	now := time.Now()
	return newClock(minTime(model.Round(0), now.Round(0)), now, speed)
}

func newClock(start, realStart time.Time, speed int) *Clock {
	c := &Clock{start: start, realStart: realStart, speed: time.Duration(max(speed, 1))}
	if lag := realStart.Round(0).Sub(start); lag > 0 && c.speed > 1 {
		// The model gains speed−1 seconds per real second.
		c.catchUp = lag / (c.speed - 1)
	}
	return c
}

// Now returns the model time in UTC, without a monotonic reading.
func (c *Clock) Now() time.Time {
	now := time.Now()
	elapsed := now.Sub(c.realStart)
	if c.speed > 1 && elapsed >= c.catchUp {
		return now.Round(0).UTC()
	}
	// elapsed < catchUp ≤ 90 days / (speed−1): elapsed·speed cannot overflow.
	return c.start.Add(elapsed * c.speed).UTC()
}

// CaughtUp reports whether the model has reached real time.
func (c *Clock) CaughtUp() bool {
	return c.speed > 1 && time.Since(c.realStart) >= c.catchUp ||
		!c.start.Before(c.realStart.Round(0))
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
