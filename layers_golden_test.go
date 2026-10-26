package main

import (
	"fmt"
	"strings"
	"testing"
)

// The layered layout is pinned by the full text of four frames of testGraph at
// 100x36: the first, the frame before a status change, the frame after it and
// the last of 90. Layered output has no growth model, so any change to
// `--layout layers` shows up here.
func TestLayersGoldenPinned(t *testing.T) {
	v := newView()
	tick := simulated(v, 15)
	v.apply(testGraph())
	pin := map[int]bool{0: true, 30: true, 31: true, 89: true}
	var b strings.Builder
	for i := 0; i < 90; i++ {
		tick()
		if i == 30 {
			ns, es := testGraph()
			ns[1].Status = "done"
			v.apply(ns, es)
		}
		f := v.frame(100, 36, 1.0/15)
		if pin[i] {
			fmt.Fprintf(&b, "=== frame %d ===\n%s\n", i, f)
		}
	}
	checkGolden(t, "layers_frames.txt", b.String())
}
