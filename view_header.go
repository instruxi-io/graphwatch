package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// headerState is the plan-level state the header shows. A nil pointer or map
// means that part is unknown (never fetched, or its last fetch failed) and its
// segment is simply left out.
type headerState struct {
	Counts     map[string]int // by_work_state of the summary
	Review     *int           // open review items
	Epoch      *int           // current epoch
	Epochs     *int           // epochs so far; Epoch of Epochs when replayed
	Recruiting *recruitingInfo
	Stream     string // live | reconnecting | reset | "" (unknown)
	Cursor     string // last frame id, seq@snapshot
}

// headerCountOrder is the work_states the header names, in order; the labels
// are the server's words.
var headerCountOrder = []struct{ key, label string }{
	{"looking_for_work", "looking for work"},
	{"claimed", "claimed"},
	{"looking_for_validation", "looking for validation"},
	{"held", "held"},
}

// cursorSeq is the seq part of a seq@snapshot cursor.
func cursorSeq(cur string) string {
	seq, _, _ := strings.Cut(cur, "@")
	return seq
}

// headerLine is one line, at most width runes, of the plan-level overlays.
// Pure: the view hands it a snapshot and draws the result.
func headerLine(s headerState, width int) string {
	var segs []string
	if s.Epoch != nil {
		if s.Epochs != nil && *s.Epochs > *s.Epoch {
			segs = append(segs, fmt.Sprintf("epoch %d of %d", *s.Epoch, *s.Epochs))
		} else {
			segs = append(segs, fmt.Sprintf("epoch %d", *s.Epoch))
		}
	}
	if s.Counts != nil {
		var parts []string
		for _, c := range headerCountOrder {
			parts = append(parts, fmt.Sprintf("%d %s", s.Counts[c.key], c.label))
		}
		segs = append(segs, strings.Join(parts, " · "))
	}
	if s.Review != nil {
		if *s.Review > 0 {
			segs = append(segs, "HELD "+strconv.Itoa(*s.Review))
		} else {
			segs = append(segs, "0 held")
		}
	}
	if r := s.Recruiting; r != nil {
		if r.Recruiting.Open {
			segs = append(segs, fmt.Sprintf("recruiting %s (max %d)", r.Recruiting.Audience, r.Recruiting.MaxParallel))
		} else {
			segs = append(segs, "not recruiting")
		}
	}
	switch s.Stream {
	case "live":
		if seq := cursorSeq(s.Cursor); seq != "" {
			segs = append(segs, "live seq "+seq)
		} else {
			segs = append(segs, "live")
		}
	case "reconnecting", "reset":
		segs = append(segs, s.Stream)
	}
	line := strings.Join(segs, "  │  ")
	if r := []rune(line); width > 0 && len(r) > width {
		line = string(r[:width])
	}
	return line
}

// overlays holds the header's state under its own lock, so a slow optional
// fetch never blocks a frame.
type overlays struct {
	mu sync.Mutex
	st headerState
}

func (o *overlays) snapshot() headerState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.st
}

func (o *overlays) set(f func(*headerState)) { o.mu.Lock(); f(&o.st); o.mu.Unlock() }

// stream records a stream frame's id and the link state it implies.
func (o *overlays) stream(ev sseEvent) {
	o.set(func(s *headerState) {
		switch ev.Event {
		case ":":
			return
		case "reset":
			s.Stream = "reset"
			return
		}
		if ev.ID != "" {
			s.Cursor = ev.ID
		}
		s.Stream = "live"
	})
}

func (o *overlays) link(status string) {
	o.set(func(s *headerState) {
		switch {
		case status == "reconnecting":
			s.Stream = "reconnecting"
		case strings.HasPrefix(status, "refused"):
			s.Stream = ""
		}
	})
}

// refresh re-reads every segment; each failure clears only its own segment.
// All reads are GETs.
func (o *overlays) refresh(ctx context.Context, c *client, graphID string) {
	counts, err := c.workStateCounts(ctx, graphID)
	if err != nil {
		counts = nil
	}
	var review *int
	if items, err := c.reviewItems(ctx, graphID); err == nil {
		n := 0
		for _, it := range items {
			if isOpenReview(it.State) {
				n++
			}
		}
		review = &n
	}
	var epoch, epochs *int
	if es, err := c.epochs(ctx, graphID); err == nil && len(es) > 0 {
		hi, cur := 0, 0
		for _, e := range es {
			hi = max(hi, e.Epoch)
			if e.Current {
				cur = e.Epoch
			}
		}
		if cur == 0 {
			cur = hi
		}
		epoch, epochs = &cur, &hi
	}
	var rec *recruitingInfo
	if r, err := c.recruiting(ctx, graphID); err == nil {
		rec = &r
	}
	o.set(func(s *headerState) {
		s.Counts, s.Review, s.Epoch, s.Epochs, s.Recruiting = counts, review, epoch, epochs, rec
	})
}

// overlayMinGap keeps event-driven refreshes from outrunning the 10 s poll.
const overlayMinGap = 2 * time.Second

// noOverlays is the --no-overlays flag.
var noOverlays bool

// startOverlays refreshes the header on the 10 s safety poll and, through the
// returned poke, on stream events, no more often than overlayMinGap.
func startOverlays(ctx context.Context, c *client, graphID string, o *overlays) (poke func()) {
	kick := make(chan struct{}, 1)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			o.refresh(ctx, c, graphID)
			select {
			case <-ctx.Done():
				return
			case <-kick:
				select {
				case <-ctx.Done():
					return
				case <-time.After(overlayMinGap):
				}
			case <-t.C:
			}
		}
	}()
	return func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}
