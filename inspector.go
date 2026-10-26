package main

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"
)

// The inspector: a selection ring that moves between nodes and a panel of what
// the API holds about the selected one. Everything it reads is a GET. The
// state it draws from (inspState) is plain data so inspectorLines and
// selectNearest test without a terminal or a server.

const inspectorTTL = 5 * time.Second

// inspState is everything the panel shows for one node.
type inspState struct {
	Node     apiNodeFull
	Run      *apiRun // the current or latest run
	Verdicts []apiVerdict
	Votes    []apiVote
	Route    *apiRoute
	RouteErr string
	RunErr   string
	Held     bool // an open review item names this node
	Loading  bool
	Now      time.Time
}

// inspNodeData is the part of a node's data object the panel reads.
type inspNodeData struct {
	Tier       string   `json:"tier"`
	TierSource string   `json:"tier_source"`
	Acceptance []string `json:"acceptance"`
}

func parseInspNodeData(raw json.RawMessage) inspNodeData {
	var d inspNodeData
	_ = json.Unmarshal(raw, &d)
	return d
}

// isHeld is the marker rule: verifying, or an open review item.
func isHeld(status string, review bool) bool { return status == "verifying" || review }

// selectNearest returns the node nearest to cur in dir (up, down, left, right)
// by screen position. Along-axis distance counts once, cross-axis twice, so a
// node straight below beats one far off to the side. cur stays put when
// nothing lies that way; an empty cur picks the top-left-most node.
func selectNearest(pos map[string][2]float64, cur, dir string) string {
	ids := make([]string, 0, len(pos))
	for id := range pos {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return cur
	}
	here, ok := pos[cur]
	if !ok {
		best := ids[0]
		for _, id := range ids {
			p, b := pos[id], pos[best]
			if p[1] < b[1] || (p[1] == b[1] && p[0] < b[0]) {
				best = id
			}
		}
		return best
	}
	best, bestScore := cur, math.Inf(1)
	for _, id := range ids {
		if id == cur {
			continue
		}
		dx, dy := pos[id][0]-here[0], pos[id][1]-here[1]
		var along, cross float64
		switch dir {
		case "up":
			along, cross = -dy, dx
		case "down":
			along, cross = dy, dx
		case "left":
			along, cross = -dx, dy
		case "right":
			along, cross = dx, dy
		default:
			return cur
		}
		if along <= 0 {
			continue
		}
		if s := along + 2*math.Abs(cross); s < bestScore {
			best, bestScore = id, s
		}
	}
	return best
}

// inspector holds the selection, the cache and the held set. The view reads it
// each frame; the key handler and the fetchers write it.
type inspector struct {
	mu    sync.Mutex
	open  bool
	sel   string
	pos   map[string][2]float64
	held  map[string]bool // nodes with an open review item
	cache map[string]*inspEntry
	// load fetches one node's state; replaced in tests.
	load func(ctx context.Context, nodeID string) inspState
	ctx  context.Context
	now  func() time.Time
}

type inspEntry struct {
	st      inspState
	at      time.Time
	pending bool
}

func newInspector() *inspector {
	return &inspector{pos: map[string][2]float64{}, held: map[string]bool{}, cache: map[string]*inspEntry{}, now: time.Now}
}

var arrowKeys = map[string]string{"up": "up", "k": "up", "down": "down", "j": "down", "left": "left", "h": "left", "right": "right", "l": "right"}

// key applies one key and reports whether the inspector consumed it. Esc is
// consumed only while the panel is open, so Esc on a closed panel still leaves.
func (in *inspector) key(k string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	if dir, ok := arrowKeys[k]; ok {
		in.sel = selectNearest(in.pos, in.sel, dir)
		if in.open {
			in.fetchLocked(in.sel, false)
		}
		return true
	}
	switch k {
	case "enter":
		if in.sel == "" {
			in.sel = selectNearest(in.pos, "", "down")
		}
		if in.sel == "" {
			return true
		}
		in.open = true
		in.fetchLocked(in.sel, false)
		return true
	case "esc":
		if in.open {
			in.open = false
			return true
		}
	}
	return false
}

// notify is the stream hook: an event for a node invalidates its cache and, if
// it is on screen in the panel, refetches it.
func (in *inspector) notify(nodeID string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if e := in.cache[nodeID]; e != nil {
		e.at = time.Time{}
	}
	if in.open && in.sel == nodeID {
		in.fetchLocked(nodeID, true)
	}
}

func (in *inspector) fetchLocked(id string, force bool) {
	e := in.cache[id]
	if e == nil {
		e = &inspEntry{st: inspState{Loading: true}}
		in.cache[id] = e
	}
	if e.pending || in.load == nil || (!force && !e.at.IsZero() && in.now().Sub(e.at) < inspectorTTL) {
		return
	}
	e.pending = true
	ctx := in.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		st := in.load(ctx, id)
		in.mu.Lock()
		e.st, e.at, e.pending = st, in.now(), false
		in.mu.Unlock()
	}()
}

func (in *inspector) setHeld(h map[string]bool) {
	in.mu.Lock()
	in.held = h
	in.mu.Unlock()
}

// snapshot returns the selected node's state for drawing, or false when the
// panel is closed.
func (in *inspector) snapshot() (inspState, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.open {
		return inspState{}, false
	}
	e := in.cache[in.sel]
	if e == nil {
		return inspState{Loading: true}, true
	}
	st := e.st
	st.Held = st.Held || in.held[in.sel]
	// An expired entry is shown as is while the refetch runs; the view asks again.
	if !e.pending && (e.at.IsZero() || in.now().Sub(e.at) >= inspectorTTL) {
		in.fetchLocked(in.sel, false)
	}
	return st, true
}

// loadFromAPI is the real loader: node, its latest run with verdicts and
// votes, and its route. Each failure lands in the panel, not in a crash.
func loadFromAPI(c *client, graphID string) func(context.Context, string) inspState {
	return func(ctx context.Context, nodeID string) inspState {
		var st inspState
		if nodes, err := c.nodesFull(ctx, graphID); err == nil {
			for _, n := range nodes {
				if n.ID == nodeID {
					st.Node = n
				}
			}
		}
		runs, _, err := c.nodeRuns(ctx, graphID, nodeID)
		switch {
		case err != nil:
			st.RunErr = err.Error()
		case len(runs) > 0:
			latest := runs[0]
			for _, r := range runs {
				if r.Attempt > latest.Attempt {
					latest = r
				}
			}
			st.Run = &latest
			if vs, err := c.runVerdicts(ctx, graphID, nodeID, latest.ID); err == nil {
				st.Verdicts = vs
			}
			if vt, err := c.runVotes(ctx, graphID, nodeID, latest.ID); err == nil {
				st.Votes = vt
			}
		}
		if r, err := c.nodeRoute(ctx, graphID, nodeID); err == nil {
			st.Route = &r
		} else {
			st.RouteErr = err.Error()
		}
		return st
	}
}

// startInspector wires the inspector to a watched graph: the loader, the held
// set (open review items, refreshed on every event and on a slow tick) and the
// stream hook. It only reads.
func startInspector(ctx context.Context, c *client, graphID string, v *view) {
	in := v.insp
	in.mu.Lock()
	in.ctx, in.load = ctx, loadFromAPI(c, graphID)
	in.mu.Unlock()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			if items, err := c.reviewItems(ctx, graphID); err == nil {
				h := map[string]bool{}
				for _, it := range items {
					if it.State == "open" || it.State == "" {
						h[it.NodeID] = true
					}
				}
				in.setHeld(h)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
