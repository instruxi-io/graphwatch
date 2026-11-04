package main

import (
	"context"
	"testing"
	"time"
)

func ticked(ch <-chan struct{}, within time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(within):
		return false
	}
}

// One clock, every subscriber: the three watch-view loops tick together.
func TestPacerTicksEverySubscriber(t *testing.T) {
	p := &pacer{down: 20 * time.Millisecond, live: time.Hour, wake: make(chan struct{}, 1)}
	a, b, c := p.subscribe(), p.subscribe(), p.subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.run(ctx)
	for i, ch := range []<-chan struct{}{a, b, c} {
		if !ticked(ch, time.Second) {
			t.Fatalf("subscriber %d missed the tick", i)
		}
	}
}

// Live stream: the poll slows to the live interval. Losing the stream ticks at
// once and goes back to the fast interval.
func TestPacerSlowsWhileLiveAndTicksOnDrop(t *testing.T) {
	p := &pacer{down: 20 * time.Millisecond, live: time.Hour, wake: make(chan struct{}, 1)}
	ch := p.subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.run(ctx)

	if !ticked(ch, time.Second) {
		t.Fatal("no tick while the stream is down")
	}
	p.frame() // stream connected
	// Drain anything already queued, then expect silence.
	ticked(ch, 30*time.Millisecond)
	if ticked(ch, 150*time.Millisecond) {
		t.Fatal("ticked at the down interval while live")
	}

	p.linkStatus("reconnecting")
	if !ticked(ch, 15*time.Millisecond) {
		t.Fatal("losing the stream should tick at once")
	}
	if !ticked(ch, time.Second) {
		t.Fatal("down again: should be back on the fast interval")
	}
}

func TestPacerLinkStatus(t *testing.T) {
	p := newPacer()
	for _, s := range []string{"reconnecting", "refused 401", "refused: no headers"} {
		p.frame()
		p.linkStatus(s)
		if _, live := p.state(); live {
			t.Fatalf("%q should mean down", s)
		}
	}
	if d, _ := p.state(); d != pollWhileDown {
		t.Fatalf("down interval = %v", d)
	}
	p.frame()
	if d, live := p.state(); !live || d != pollWhileLive {
		t.Fatalf("live interval = %v live=%v", d, live)
	}
}
