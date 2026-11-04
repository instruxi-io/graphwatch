package main

import (
	"context"
	"sync"
	"time"
)

// The watch view's safety-net cadence. The stream is the clock (every event
// means refetch); the poll only covers what a stream might miss. While the
// stream is connected that is very little, so the poll slows down; while it is
// down the poll is all there is, so it runs at the old 10 s.
const (
	pollWhileDown = 10 * time.Second
	pollWhileLive = 60 * time.Second
)

// pacer is the ONE safety-net clock for a watched graph. The node/edge reader,
// the header overlays and the inspector's review set used to run three
// independent 10 s tickers, so an idle open viewer sent ~50 requests a minute
// whether or not anything changed. They now subscribe here and tick together.
//
// A tick is delivered to every subscriber without blocking: a subscriber still
// busy with the last one simply misses it (each channel holds one). Losing the
// stream ticks at once, since events may have gone by unseen.
type pacer struct {
	down, live time.Duration

	mu     sync.Mutex
	isLive bool
	subs   []chan struct{}
	wake   chan struct{}
}

func newPacer() *pacer {
	return &pacer{down: pollWhileDown, live: pollWhileLive, wake: make(chan struct{}, 1)}
}

// subscribe returns a channel that receives each tick. Call it before run.
func (p *pacer) subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	p.mu.Lock()
	p.subs = append(p.subs, ch)
	p.mu.Unlock()
	return ch
}

// setLive records the stream's state; a change re-arms the clock.
func (p *pacer) setLive(live bool) {
	p.mu.Lock()
	changed := p.isLive != live
	p.isLive = live
	p.mu.Unlock()
	if changed {
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
}

func (p *pacer) state() (interval time.Duration, live bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isLive {
		return p.live, true
	}
	return p.down, false
}

func (p *pacer) broadcast() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ch := range p.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// run drives the clock until ctx ends.
func (p *pacer) run(ctx context.Context) {
	for {
		d, _ := p.state()
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-p.wake:
			t.Stop()
			if _, live := p.state(); !live {
				p.broadcast() // the stream just dropped: read now, not in up to 60 s
			}
		case <-t.C:
			p.broadcast()
		}
	}
}

// frame and linkStatus turn the stream's callbacks into live/down:
// any frame (including the server's ready and ":" keepalives) means connected;
// "reconnecting" or a refusal means not.
func (p *pacer) frame()              { p.setLive(true) }
func (p *pacer) linkStatus(s string) { p.setLive(s == "live") }
