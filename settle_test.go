package main

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"
)

// bigDAG is a seeded random plan shaped like a real large one that never
// settled: 255 nodes, a wide first layer of 65 roots narrowing to a long tail
// of 37 layers, most nodes with one prerequisite and a few hubs with many.
func bigDAG(seed uint64) ([]apiNode, []apiEdge) {
	r := rand.New(rand.NewPCG(seed, 7))
	widths := []int{65, 34, 23, 27, 19, 10, 8, 5, 4, 4, 3, 4, 4, 3, 3, 3, 4, 5, 5, 2, 2, 2, 2, 1, 2, 2, 2, 2, 1, 2, 1, 1, 2, 1, 1, 1, 1}
	at := time.Unix(1_700_000_000, 0)
	var ns []apiNode
	var es []apiEdge
	var layer [][]string
	all := []string{}
	for d, w := range widths {
		var row []string
		for k := 0; k < w; k++ {
			id := fmt.Sprintf("n%03d", len(all)+len(row))
			// Batch imports: nodes share created times in runs.
			ns = append(ns, apiNode{ID: id, Key: id, Status: "active", CreatedAt: at.Add(time.Duration(len(ns)/4) * time.Second)})
			row = append(row, id)
			if d == 0 {
				continue
			}
			prev := layer[d-1]
			es = append(es, apiEdge{FromNodeID: id, ToNodeID: prev[r.IntN(len(prev))], Type: "requires"})
			extra := 0
			switch x := r.IntN(100); {
			case x < 60:
			case x < 75:
				extra = 1
			case x < 92:
				extra = 2 + r.IntN(3)
			default:
				extra = 5 + r.IntN(30)
			}
			for ; extra > 0; extra-- {
				es = append(es, apiEdge{FromNodeID: id, ToNodeID: all[r.IntN(len(all))], Type: "requires"})
			}
		}
		layer = append(layer, row)
		all = append(all, row...)
	}
	return ns, es
}

// Seeds 10, 14, 16 and 19 are ones whose old per-frame ordering cycled.
//
// A big graph whose topology is not changing must stop moving and, once it
// has, send the terminal (nearly) nothing. The layer order used to be
// recomputed every frame from the frame before, and on graphs like this it
// cycled for good: the view glided forever at ~400 KB/s of repaint.
func TestBigGraphSettles(t *testing.T) {
	for _, seed := range []uint64{10, 14, 16, 19} {
		ns, es := bigDAG(seed)
		v := newView()
		v.apply(ns, es)
		clk := time.Unix(0, 0)
		v.clock, v.started = func() time.Time { return clk }, clk
		var scr screen
		var prev map[string]float64
		moved, sent := 0, 0
		for f := 1; f <= 600; f++ { // 40 s at 15 fps
			clk = clk.Add(time.Second / 15)
			out := scr.diff(v.frame(200, 60, 1.0/15))
			if f <= 450 {
				prev = map[string]float64{}
				for id, x := range v.prevX {
					prev[id] = x
				}
				continue
			}
			sent += len(out)
			for id, x := range v.prevX {
				if prev[id] != x {
					moved++
				}
				prev[id] = x
			}
		}
		if moved > 0 {
			t.Errorf("seed %d: layout targets still changing after 30 s (%d moves in the last 10 s)", seed, moved)
		}
		// The header clock may tick once a second; anything more is motion.
		if sent > 2000 {
			t.Errorf("seed %d: sent %d bytes in the last 10 s of a settled graph", seed, sent)
		}
	}
}
