package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"testing"
	"time"
)

// encode serialises a grown structure in a fixed order, so two structures
// can be compared byte for byte.
func (g *grown) encode() []byte {
	var b bytes.Buffer
	owners := make([]string, 0, len(g.owner))
	for id := range g.owner {
		owners = append(owners, id)
	}
	sort.Strings(owners)
	for _, id := range owners {
		b.WriteString(id)
		for _, d := range g.owner[id] {
			_ = binary.Write(&b, binary.LittleEndian, d)
		}
	}
	keys := make([][2]string, 0, len(g.path))
	for k := range g.path {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+"\x00"+keys[i][1] < keys[j][0]+"\x00"+keys[j][1] })
	for _, k := range keys {
		b.WriteString(k[0] + ">" + k[1])
		_ = binary.Write(&b, binary.LittleEndian, g.path[k])
	}
	fmt.Fprintf(&b, "%d/%d/%d/%d", g.dots, g.budget, g.branches, g.edges)
	return b.Bytes()
}

func demoView() *view {
	v := newView()
	v.mycelium = true
	ns, es := demoPlan()
	setStatus(ns, "done", "spec", "schema", "infra")
	setStatus(ns, "running", "parse")
	v.apply(ns, es)
	return v
}

func TestMyceliumGrowthIsDeterministicAcrossRunsAndFrames(t *testing.T) {
	a, b := demoView(), demoView()
	a.frame(100, 36, 1.0/15)
	b.frame(100, 36, 1.0/15)
	first := a.grown.encode()
	if !bytes.Equal(first, b.grown.encode()) {
		t.Fatal("two runs grew different hyphae")
	}
	g0 := a.grown
	for i := 0; i < 30; i++ {
		a.frame(100, 36, 1.0/15)
	}
	if a.grown != g0 || !bytes.Equal(first, a.grown.encode()) {
		t.Fatal("the hyphae were re-grown between frames")
	}
	// A refresh that changes only statuses keeps the cached growth.
	ns, es := demoPlan()
	setStatus(ns, "done", "spec", "schema", "infra", "parse")
	a.apply(ns, es)
	a.frame(100, 36, 1.0/15)
	if a.grown != g0 {
		t.Fatal("a status-only refresh re-grew the hyphae")
	}
	t.Logf("%d bytes, %d dots", len(first), a.grown.dots)
}

func TestMyceliumGrowsLateralBranchesUnderBudget(t *testing.T) {
	v := demoView()
	v.frame(100, 36, 1.0/15)
	g := v.grown
	if g.edges == 0 || g.branches <= g.edges {
		t.Errorf("branches %d, edges %d: want more lateral branches than edges", g.branches, g.edges)
	}
	if g.dots > g.budget || g.budget > budgetCap {
		t.Errorf("dots %d over budget %d (cap %d)", g.dots, g.budget, budgetCap)
	}
	// Every edge's primary hypha reaches its dependent.
	for k, p := range g.path {
		if len(p) == 0 {
			t.Errorf("edge %s->%s grew no primary hypha", k[0], k[1])
			continue
		}
		end := p[len(p)-1]
		ex, ey := float64(end&0xffff), float64(end>>16)
		tx, ty := v.myc.x[k[1]]*2+1, v.myc.y[k[1]]*4+2
		if dx, dy := ex-tx, ey-ty; dx*dx+dy*dy > 9 {
			t.Errorf("edge %s->%s ends %.0f,%.0f, dependent at %.0f,%.0f", k[0], k[1], ex, ey, tx, ty)
		}
	}
	t.Logf("dots %d / budget %d, branches %d, edges %d", g.dots, g.budget, g.branches, g.edges)
}

// A 100-node graph grows once, fast, and stays under the budget.
func TestMyceliumHundredNodesIsBounded(t *testing.T) {
	var ns []apiNode
	var es []apiEdge
	for i := 0; i < 100; i++ {
		id := "n" + strconv.Itoa(i)
		ns = append(ns, apiNode{ID: id, Key: id, Status: "done"})
		if i >= 3 {
			es = append(es, apiEdge{id, "n" + strconv.Itoa((i-3)/2), "requires"})
			if i%4 == 0 {
				es = append(es, apiEdge{id, "n" + strconv.Itoa(i/3), "requires"})
			}
		}
	}
	v := newView()
	v.mycelium = true
	v.apply(ns, es)
	start := time.Now()
	v.frame(200, 60, 1.0/15)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("growing 100 nodes took %v", d)
	}
	if v.grown.dots > v.grown.budget {
		t.Errorf("dots %d over budget %d", v.grown.dots, v.grown.budget)
	}
}

// A mycelium frame costs about the same allocations every frame: the
// hyphae are read from the cache, never re-grown or appended to.
func TestMyceliumFrameAllocationsDoNotGrow(t *testing.T) {
	v := demoView()
	tick := simulated(v, 15)
	for i := 0; i < 60; i++ {
		tick()
		v.frame(100, 36, 1.0/15)
	}
	run := func() float64 {
		return testing.AllocsPerRun(50, func() { tick(); v.frame(100, 36, 1.0/15) })
	}
	a := run()
	for i := 0; i < 300; i++ {
		tick()
		v.frame(100, 36, 1.0/15)
	}
	// The frame's own strings vary by a few allocations with what it says
	// (a spinner glyph, a count); growth would scale with frames drawn.
	if b := run(); b > a*1.05+4 {
		t.Errorf("allocations per frame grew from %.0f to %.0f", a, b)
	}
}

// The offline demo works its whole plan, drives the view to completion, and
// then nothing but the finale banner (row 3, which pulses until graphwatch
// exits) is ever redrawn.
func TestMyceliumLocalDemoCompletesAndQuiets(t *testing.T) {
	const w, h, fps = 100, 36, 15
	v := newView()
	v.mycelium = true
	tick := simulated(v, fps)
	d := newLocalDemo(4, 1)
	v.apply(d.ns, d.es)
	var scr screen
	pos := regexp.MustCompile(`\x1b\[(\d+);\d+H`)
	moving, n := 0, 0
	for i := 0; i < 120*fps; i++ {
		tick()
		if evs, changed := d.step(v.clock()); changed {
			v.apply(d.ns, d.es)
			for _, e := range evs {
				v.event(e)
			}
		}
		out := scr.diff(v.frame(w, h, 1.0/fps))
		if i < 110*fps {
			continue
		}
		n++
		for _, m := range pos.FindAllStringSubmatch(out, -1) {
			if m[1] != "3" {
				moving++
				break
			}
		}
	}
	if _, ok := v.completed(); !ok || !d.complete() {
		t.Fatal("the offline demo did not complete its plan in 120 s")
	}
	if moving > 0 {
		t.Errorf("%d of %d frames after completion redrew more than the finale banner", moving, n)
	}
}
