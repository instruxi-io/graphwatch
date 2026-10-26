package main

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"time"
)

// demoPlan is the offline mycelium demo's plan: three roots feeding a
// depth-four DAG (depths 0..3), with joins, so `--demo --layout mycelium`
// shows a colony growing without a live graph. Statuses start pending.
func demoPlan() ([]apiNode, []apiEdge) {
	keys := []struct {
		k   string
		pre []string
	}{
		{"spec", nil}, {"schema", nil}, {"infra", nil},
		{"parse", []string{"spec"}}, {"lint", []string{"spec"}}, {"migrate", []string{"schema"}},
		{"seed", []string{"schema"}}, {"cluster", []string{"infra"}}, {"cache", []string{"infra", "schema"}},
		{"compile", []string{"parse", "lint"}}, {"index", []string{"migrate", "seed"}},
		{"deploy", []string{"cluster", "cache"}}, {"probe", []string{"cluster"}},
		{"bundle", []string{"compile"}}, {"embed", []string{"index", "cache"}},
		{"ship", []string{"deploy", "compile"}}, {"audit", []string{"probe", "seed"}},
	}
	var ns []apiNode
	var es []apiEdge
	for _, n := range keys {
		ns = append(ns, apiNode{ID: n.k, Key: n.k, Status: "pending"})
		for _, p := range n.pre {
			es = append(es, apiEdge{n.k, p, "requires"})
		}
	}
	return ns, es
}

// localDemo works demoPlan in-process: up to `workers` nodes run at once,
// each for a few seconds, and a node becomes claimable when its
// prerequisites are done. It drives the view exactly as a live graph would,
// through apply and stream events, so what a person sees is the real view.
type localDemo struct {
	ns      []apiNode
	es      []apiEdge
	pre     map[string][]string
	until   map[string]time.Time
	workers int
	rng     *rand.Rand
	seq     int64
}

func newLocalDemo(workers int, seed uint64) *localDemo {
	d := &localDemo{pre: map[string][]string{}, until: map[string]time.Time{}, workers: max(workers, 1),
		rng: rand.New(rand.NewPCG(seed, 11))}
	d.ns, d.es = demoPlan()
	for _, e := range d.es {
		d.pre[e.FromNodeID] = append(d.pre[e.FromNodeID], e.ToNodeID)
	}
	return d
}

func (d *localDemo) status(id string) string {
	for _, n := range d.ns {
		if n.ID == id {
			return n.Status
		}
	}
	return ""
}

func (d *localDemo) complete() bool {
	for _, n := range d.ns {
		if n.Status != "done" {
			return false
		}
	}
	return true
}

// step advances the plan to now. It returns the stream events that
// happened, and whether any status changed.
func (d *localDemo) step(now time.Time) ([]sseEvent, bool) {
	var evs []sseEvent
	ev := func(verb, key, status string) {
		d.seq++
		md := map[string]string{"node": key}
		if status != "" {
			md["status"] = status
		}
		b, _ := json.Marshal(streamRow{Seq: d.seq, Verb: verb, Metadata: md})
		evs = append(evs, sseEvent{Event: "message", Data: b})
	}
	changed, running := false, 0
	for i := range d.ns {
		n := &d.ns[i]
		if n.Status == "running" && now.After(d.until[n.ID]) {
			n.Status, changed = "done", true
			ev("run.finished", n.Key, "succeeded")
		}
		if n.Status == "running" {
			running++
		}
	}
	for i := range d.ns {
		n := &d.ns[i]
		if running >= d.workers || n.Status != "pending" {
			continue
		}
		ready := true
		for _, p := range d.pre[n.ID] {
			if d.status(p) != "done" {
				ready = false
			}
		}
		if ready {
			n.Status, changed = "running", true
			d.until[n.ID] = now.Add(time.Duration(1800+d.rng.IntN(3200)) * time.Millisecond)
			running++
			ev("run.started", n.Key, "")
		}
	}
	if changed && d.complete() {
		ev("graph.completed", "", "")
	}
	return evs, changed
}

// runLocalDemo is `--demo --layout mycelium`: the colony grows on a plan
// worked in-process, with no API, credential or network.
func runLocalDemo(ctx context.Context, v *view, workers, fps int, stay bool) {
	d := newLocalDemo(workers, uint64(time.Now().UnixNano()))
	v.title = "mycelium demo (offline)"
	v.link = "offline"
	v.apply(d.ns, d.es)

	os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J")
	restore := func() { os.Stdout.WriteString("\x1b[0m\x1b[?25h\x1b[?1049l") }
	defer restore()
	w, h := termSize()
	sizeT := time.NewTicker(time.Second)
	defer sizeT.Stop()
	frameT := time.NewTicker(time.Second / time.Duration(max(fps, 1)))
	defer frameT.Stop()
	last := time.Now()
	var scr screen
	for {
		select {
		case <-ctx.Done():
			return
		case <-sizeT.C:
			if nw, nh := termSize(); nw != w || nh != h {
				w, h = nw, nh
				scr.reset()
			}
		case now := <-frameT.C:
			if evs, changed := d.step(now); changed {
				v.apply(d.ns, d.es)
				for _, e := range evs {
					v.event(e)
				}
			}
			dt := now.Sub(last).Seconds()
			last = now
			if out := scr.diff(v.frame(w, h, dt)); out != "" {
				os.Stdout.WriteString(out)
			}
			if at, ok := v.completed(); ok && !stay && time.Since(at) > 6*time.Second {
				return
			}
		}
	}
}
