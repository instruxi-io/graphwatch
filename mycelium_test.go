package main

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

// myceliumGraph is 3 roots feeding a depth-4 DAG (depths 0..3).
func myceliumGraph() ([]apiNode, []apiEdge) {
	var ns []apiNode
	var es []apiEdge
	add := func(id, st string) { ns = append(ns, apiNode{ID: id, Key: id, Status: st}) }
	dep := func(from, to string) { es = append(es, apiEdge{from, to, "requires"}) }
	add("r1", "done")
	add("r2", "done")
	add("r3", "running")
	add("a", "pending")
	add("b", "pending")
	add("c", "pending")
	add("d", "pending")
	add("e", "pending")
	dep("a", "r1")
	dep("b", "r2")
	dep("c", "r3")
	dep("c", "r1")
	dep("d", "a")
	dep("d", "b")
	dep("e", "d")
	dep("e", "c")
	return ns, es
}

func layoutOf(t *testing.T) (myc, map[string]int) {
	t.Helper()
	v := newView()
	v.apply(myceliumGraph())
	d := layers(v.nodes)
	return mycelium(v.nodes, d, 100, 4, 30), d
}

func TestMyceliumRootsNearerCentreThanDependents(t *testing.T) {
	m, d := layoutOf(t)
	maxD := 0
	for _, x := range d {
		maxD = max(maxD, x)
	}
	if maxD != 3 {
		t.Fatalf("test graph depth = %d, want 3 (four layers)", maxD)
	}
	for id, dd := range d {
		if dd == 0 {
			continue
		}
		for _, r := range []string{"r1", "r2", "r3"} {
			if m.rad[r] >= m.rad[id] {
				t.Errorf("root %s radius %.2f not inside %s (depth %d) radius %.2f", r, m.rad[r], id, dd, m.rad[id])
			}
		}
	}
	// And in cell space: roots really are closer to the centre point.
	cx, cy := 50.0, 17.0
	dist := func(id string) float64 {
		dx, dy := m.x[id]-cx, (m.y[id]-cy)*2
		return dx*dx + dy*dy
	}
	if dist("r1") >= dist("e") {
		t.Errorf("root r1 is not nearer the centre than e")
	}
}

func TestMyceliumIsDeterministicAcrossRunsAndFrames(t *testing.T) {
	a, _ := layoutOf(t)
	b, _ := layoutOf(t)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two runs gave different layouts")
	}
	v := newView()
	v.mycelium = true
	tick := simulated(v, 15)
	v.apply(myceliumGraph())
	tick()
	v.frame(100, 36, 1.0/15)
	first := v.myc
	for i := 0; i < 5; i++ {
		tick()
		v.frame(100, 36, 1.0/15)
	}
	if !reflect.DeepEqual(first, v.myc) {
		t.Fatal("layout moved between frames")
	}
	if !reflect.DeepEqual(first.x, a.x) && first.x == nil {
		t.Fatal("no layout cached")
	}
}

func TestMyceliumRingsScaleToFit(t *testing.T) {
	var ns []apiNode
	var es []apiEdge
	for i := 0; i < 40; i++ {
		ns = append(ns, apiNode{ID: "n" + strconv.Itoa(i), Key: "k", Status: "pending"})
		if i > 0 {
			es = append(es, apiEdge{"n" + strconv.Itoa(i), "n" + strconv.Itoa(i-1), "requires"})
		}
	}
	v := newView()
	v.apply(ns, es)
	m := mycelium(v.nodes, layers(v.nodes), 100, 4, 30)
	for id, r := range m.rad {
		if r > 1.0001 {
			t.Errorf("%s radius %.2f beyond the screen", id, r)
		}
	}
}

func TestMyceliumFrameRenders(t *testing.T) {
	v := newView()
	v.mycelium = true
	v.apply(myceliumGraph())
	out := v.frame(100, 36, 0.05)
	if len(out) == 0 {
		t.Fatal("empty frame")
	}
}

func TestMyceliumQuietGraphSettlesToNoOutput(t *testing.T) {
	const w, h, fps = 100, 36, 15
	v := newView()
	v.mycelium = true
	tick := simulated(v, fps)
	v.apply(myceliumGraph())
	// A node finishes: its hyphae grow for about a second, then nothing.
	ns, es := myceliumGraph()
	ns[2].Status = "done"
	for i := 0; i < 3; i++ {
		tick()
	}
	var scr screen
	quiet, n := 0, 0
	for i := 0; i < 40*fps; i++ {
		tick()
		if i == fps {
			v.apply(ns, es)
		}
		out := scr.diff(v.frame(w, h, 1.0/fps))
		if i >= 30*fps {
			n++
			if out == "" {
				quiet++
			}
		}
	}
	if frac := float64(quiet) / float64(n); frac < 0.85 {
		t.Errorf("after growth only %.0f%% of frames sent nothing", frac*100)
	}
	t.Log(fmt.Sprintf("settled: %d/%d empty", quiet, n))
}

func TestMyceliumGrowthPlaysOnceThenStops(t *testing.T) {
	const w, h, fps = 100, 36, 15
	v := newView()
	v.mycelium = true
	tick := simulated(v, fps)
	v.apply(myceliumGraph())
	var scr screen
	for i := 0; i < 5*fps; i++ {
		tick()
		scr.diff(v.frame(w, h, 1.0/fps))
	}
	ns, es := myceliumGraph()
	ns[2].Status = "done"
	v.apply(ns, es)
	grew := 0
	for i := 0; i < 40*fps; i++ {
		tick()
		out := scr.diff(v.frame(w, h, 1.0/fps))
		if out != "" && i < 2*fps {
			grew++
		}
	}
	if grew < 3 {
		t.Errorf("hyphae did not visibly extend (%d changed frames in 2 s)", grew)
	}
}

func TestMyceliumCrowdedGraphSettles(t *testing.T) {
	var ns []apiNode
	var es []apiEdge
	ns = append(ns, apiNode{ID: "root", Key: "root", Status: "done"})
	for i := 0; i < 60; i++ {
		id := "n" + strconv.Itoa(i)
		st := "done"
		if i%9 == 0 {
			st = "running"
		}
		ns = append(ns, apiNode{ID: id, Key: "a-rather-long-node-key-" + strconv.Itoa(i), Status: st})
		es = append(es, apiEdge{id, "root", "requires"})
	}
	const w, h, fps = 100, 36, 15
	for trial := 0; trial < 3; trial++ {
		v := newView()
		v.mycelium = true
		tick := simulated(v, fps)
		v.apply(ns, es)
		var scr screen
		quiet, n := 0, 0
		for i := 0; i < 40*fps; i++ {
			tick()
			out := scr.diff(v.frame(w, h, 1.0/fps))
			if i >= 30*fps {
				n++
				if out == "" {
					quiet++
				}
			}
		}
		if frac := float64(quiet) / float64(n); frac < 0.85 {
			t.Fatalf("trial %d: only %.0f%% of settled frames were empty", trial, frac*100)
		}
	}
}
