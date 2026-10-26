package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var demoWords = []string{
	"parse", "lint", "compile", "bundle", "index", "embed", "rank", "fetch", "verify", "sign",
	"migrate", "seed", "cache", "shard", "render", "probe", "trace", "audit", "pack", "tune",
}

// demo grows a plan while workers run it, so the DAG is being added to at the
// top of its frontier and worked at the bottom at the same time.
type demo struct {
	c        *client
	graphID  string
	size     int
	workers  int
	failRate float64
	rng      *rand.Rand
	mu       sync.Mutex
	keys     []string // creation order
	planned  atomic.Bool
	logf     func(string, rgb)
}

// create makes the demo graph, with its first node, through POST /graphs/import
// rather than POST /graphs. An OAuth sign-in (GRAPH_AUTH_HELPER) carries
// enforcer:graph-graph-import.write and may not carry graph-graphs.write
// (the sign-in must include the graph-graph-import.write scope). Who a plan is shared
// with is the separate graph-sharing.write, an access decision the platform
// leaves to a person. Import creates the graph and its nodes in one transaction under the
// narrower scope, and works the same with an API key.
func (d *demo) create(ctx context.Context) error {
	var g struct{ Data apiGraph }
	slug := fmt.Sprintf("graphwatch-%d", time.Now().Unix())
	doc := map[string]any{
		"slug": slug, "name": "graphwatch demo", "mode": "dag",
		"nodes": []map[string]any{{"key": "plan", "title": "plan"}},
	}
	body := map[string]any{"document": doc, "slug": slug, "name": "graphwatch demo"}
	if err := d.c.do(ctx, http.MethodPost, "/graphs/import", body, &g); err != nil {
		return err
	}
	d.graphID = g.Data.ID
	// The imported root node is part of the plan the planner grows from, as
	// add() would have recorded it.
	d.mu.Lock()
	d.keys = append(d.keys, "plan")
	d.mu.Unlock()
	return nil
}

// add creates a node and its prerequisite edges without ever exposing it to
// the frontier half-built: `blocked` is claimable, so it is created
// `cancelled` (not claimable), wired, then released as `pending`.
func (d *demo) add(ctx context.Context, key string, prereqs []string) error {
	// Each step is an upsert or a status set, so each retries on its own.
	var n struct{ Data apiNode }
	if err := retry(ctx, func() error {
		return d.c.do(ctx, http.MethodPost, "/graphs/"+d.graphID+"/nodes",
			map[string]any{"key": key, "type": "task", "title": key, "status": "cancelled"}, &n)
	}); err != nil {
		return err
	}
	for _, p := range prereqs {
		if err := retry(ctx, func() error {
			return d.c.do(ctx, http.MethodPost, "/graphs/"+d.graphID+"/edges",
				map[string]any{"from_key": key, "to_key": p, "type": "requires"}, nil)
		}); err != nil {
			return err
		}
	}
	if err := retry(ctx, func() error {
		return d.c.do(ctx, http.MethodPost, "/graphs/"+d.graphID+"/nodes/"+n.Data.ID+"/status",
			map[string]any{"status": "pending"}, nil)
	}); err != nil {
		return err
	}
	d.mu.Lock()
	d.keys = append(d.keys, key)
	d.mu.Unlock()
	return nil
}

// plan fans out from recent nodes in bursts, sometimes joining two branches,
// then closes the plan with one node that depends on every leaf.
func (d *demo) plan(ctx context.Context) {
	defer d.planned.Store(true)
	children := map[string]int{}
	for i := 1; ; {
		d.mu.Lock()
		n := len(d.keys)
		keys := append([]string(nil), d.keys...)
		d.mu.Unlock()
		if n >= d.size || ctx.Err() != nil {
			break
		}
		// Fan out from one of the most recent nodes.
		parent := keys[max(0, n-1-d.rng.IntN(min(n, 6)))]
		burst := 1 + d.rng.IntN(3)
		for b := 0; b < burst && n+b < d.size; b++ {
			key := fmt.Sprintf("%s-%d", demoWords[d.rng.IntN(len(demoWords))], i)
			i++
			pre := []string{parent}
			if n > 4 && d.rng.Float64() < 0.3 { // join another branch
				if other := keys[d.rng.IntN(n)]; other != parent {
					pre = append(pre, other)
				}
			}
			if err := d.add(ctx, key, pre); err != nil {
				// Skip it and carry on: a node left `cancelled` is harmless.
				d.logf("planner: skipped "+key+" — "+err.Error(), rgb{240, 80, 64})
				continue
			}
			for _, p := range pre {
				children[p]++
			}
		}
		sleepCtx(ctx, time.Duration(500+d.rng.IntN(900))*time.Millisecond)
	}
	d.mu.Lock()
	var leaves []string
	for _, k := range d.keys {
		if children[k] == 0 {
			leaves = append(leaves, k)
		}
	}
	d.mu.Unlock()
	if err := d.add(ctx, "ship", leaves); err != nil {
		d.logf("planner: "+err.Error(), rgb{240, 80, 64})
	}
}

// work is one harness: claim, work a while, sometimes remember something,
// report. A failed node goes back to the frontier and is retried.
func (d *demo) work(ctx context.Context, id int, done func() bool) {
	runner := fmt.Sprintf("graphwatch-%d", id)
	for ctx.Err() == nil && !done() {
		var claim struct {
			Data []struct {
				NodeID string `json:"node_id"`
				RunID  string `json:"run_id"`
				Key    string `json:"key"`
			}
		}
		if err := d.c.do(ctx, http.MethodPost, "/graphs/"+d.graphID+"/frontier/claim",
			map[string]any{"limit": 1, "runner": runner}, &claim); err != nil || len(claim.Data) == 0 {
			sleepCtx(ctx, time.Duration(400+d.rng.IntN(600))*time.Millisecond)
			continue
		}
		n := claim.Data[0]
		sleepCtx(ctx, time.Duration(1200+d.rng.IntN(3200))*time.Millisecond)
		if d.rng.Float64() < 0.25 {
			_ = d.c.do(ctx, http.MethodPost, "/graphs/"+d.graphID+"/nodes/"+n.NodeID+"/observations",
				map[string]any{"body": n.Key + " took the scenic route", "source": runner}, nil)
		}
		status := "succeeded"
		if d.rng.Float64() < d.failRate {
			status = "failed"
		}
		// Reporting the same outcome twice answers 200, so this is safe to retry.
		// Without it one 503 left the run `running` until its lease lapsed.
		_ = retry(ctx, func() error {
			return d.c.do(ctx, http.MethodPost, "/graphs/"+d.graphID+"/nodes/"+n.NodeID+"/runs/"+n.RunID+"/complete",
				map[string]any{"status": status}, nil)
		})
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
