package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pagedAPI serves /graphs/g/nodes and /graphs/g/edges as the real list
// endpoints do (limit, offset, meta.total, newest first is irrelevant here),
// each request taking delay, and counts requests and the most ever in flight.
type pagedAPI struct {
	*httptest.Server
	reqs, inFlight, peak atomic.Int32
	noTotal              bool
	failNodes            atomic.Bool
}

func newPagedAPI(t *testing.T, nNodes, nEdges int, delay time.Duration) *pagedAPI {
	t.Helper()
	p := &pagedAPI{}
	nodes := make([]map[string]any, nNodes)
	for i := range nodes {
		nodes[i] = map[string]any{"id": fmt.Sprintf("n%04d", i), "key": fmt.Sprintf("k-%d", i), "status": "done", "created_at": time.Unix(1_700_000_000, 0)}
	}
	edges := make([]map[string]any, nEdges)
	for i := range edges {
		edges[i] = map[string]any{"from_node_id": fmt.Sprintf("n%04d", (i+1)%nNodes), "to_node_id": fmt.Sprintf("n%04d", i%max(nNodes-1, 1)), "type": "requires"}
	}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.reqs.Add(1)
		if n := p.inFlight.Add(1); n > p.peak.Load() {
			p.peak.Store(n)
		}
		defer p.inFlight.Add(-1)
		time.Sleep(delay)
		var all []map[string]any
		switch {
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			if p.failNodes.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"code": "boom"}})
				return
			}
			all = nodes
		case strings.HasSuffix(r.URL.Path, "/edges"):
			all = edges
		default:
			http.NotFound(w, r)
			return
		}
		lim, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		end := min(off+lim, len(all))
		page := []map[string]any{}
		if off < len(all) {
			page = all[off:end]
		}
		meta := map[string]any{"limit": lim, "offset": off}
		if !p.noTotal {
			meta["total"] = len(all)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": page, "meta": meta})
	}))
	t.Cleanup(p.Close)
	return p
}

func TestPagesParallelReadsEveryRowOnceInOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rows, expect  int
		noTotal       bool
		wantReqs      int
		wantRoundsMax int // request rounds of `delay` the read may take
	}{
		{"unknown size, 450 rows: one, then the rest at once", 450, 0, false, 3, 2},
		{"expected size: every page at once", 450, 450, false, 3, 1},
		{"small graph: one request", 120, 0, false, 1, 1},
		{"exactly one full page", 200, 0, false, 1, 1},
		{"no total from the server: page until a short one", 450, 0, true, 3, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const delay = 150 * time.Millisecond
			p := newPagedAPI(t, tc.rows, 1, delay)
			p.noTotal = tc.noTotal
			c := newClient(p.URL, apiKey("k"))
			start := time.Now()
			got, err := pagesParallel[apiNode](context.Background(), c, "/graphs/g/nodes", tc.expect, nil)
			took := time.Since(start)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.rows {
				t.Fatalf("got %d rows, want %d", len(got), tc.rows)
			}
			for i, n := range got {
				if n.ID != fmt.Sprintf("n%04d", i) {
					t.Fatalf("row %d is %s: out of order or repeated", i, n.ID)
				}
			}
			if r := int(p.reqs.Load()); r != tc.wantReqs {
				t.Errorf("%d requests, want %d", r, tc.wantReqs)
			}
			if limit := time.Duration(tc.wantRoundsMax)*delay + delay/2; took > limit {
				t.Errorf("took %v, want at most %d round(s) of %v", took, tc.wantRoundsMax, delay)
			}
		})
	}
}

func TestPagesParallelCapsRequestsInFlight(t *testing.T) {
	p := newPagedAPI(t, 2000, 1, 50*time.Millisecond)
	c := newClient(p.URL, apiKey("k"))
	got, err := pagesParallel[apiNode](context.Background(), c, "/graphs/g/nodes", 2000, nil)
	if err != nil || len(got) != 2000 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	if pk := p.peak.Load(); pk > pageWorkers {
		t.Errorf("%d requests in flight at once, want at most %d", pk, pageWorkers)
	}
}

// The first load shows progress and grows the graph page by page; once the
// graph is in, the bar is gone and a failed refresh leaves the graph alone.
func TestLoadGraphFirstLoadThenRefresh(t *testing.T) {
	p := newPagedAPI(t, 450, 300, 100*time.Millisecond)
	c := newClient(p.URL, apiKey("k"))
	v := newView()
	clk := time.Unix(0, 0)
	v.clock, v.started = func() time.Time { return clk }, clk

	var sawBar, sawPartial bool
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-time.After(10 * time.Millisecond):
			}
			out := v.frame(120, 40, 0.05)
			v.mu.Lock()
			n, loaded := len(v.nodes), v.loaded
			v.mu.Unlock()
			mu.Lock()
			if strings.Contains(strings.Join(frameText(out), "\n"), "reading the plan") {
				sawBar = true
			}
			if n > 0 && n < 450 && !loaded {
				sawPartial = true
			}
			mu.Unlock()
			if loaded {
				return
			}
		}
	}()
	n, e, err := loadGraph(context.Background(), c, v, "g", [2]int{})
	<-done
	if err != nil || n != 450 || e != 300 {
		t.Fatalf("loadGraph = %d, %d, %v", n, e, err)
	}
	mu.Lock()
	if !sawBar {
		t.Error("the progress bar was never drawn during the first load")
	}
	if !sawPartial {
		t.Error("the graph never grew page by page: it appeared all at once")
	}
	mu.Unlock()
	if strings.Contains(strings.Join(frameText(v.frame(120, 40, 0.05)), "\n"), "reading the plan") {
		t.Error("the progress bar outlived the load")
	}

	p.failNodes.Store(true)
	if _, _, err := loadGraph(context.Background(), c, v, "g", [2]int{n, e}); err == nil {
		t.Fatal("refresh against a failing server must report it")
	}
	v.mu.Lock()
	kept, loading := len(v.nodes), v.load.active
	v.mu.Unlock()
	if kept != 450 || loading {
		t.Errorf("after a failed refresh: %d nodes, bar %v; want the graph kept and no bar", kept, loading)
	}
}
