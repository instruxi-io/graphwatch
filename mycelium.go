package main

import (
	"hash/fnv"
	"math"
	"sort"
)

// Mycelium layout: the DAG as a radial growth. Roots sit on a small inner ring
// at the centre; a node's radius grows with its depth, and its angle is the
// mean angle of its prerequisites plus an offset seeded by its id, so siblings
// fan out and the picture is identical every frame and every run. Nothing here
// reads a clock or a random source.

// myc is one computed layout. Positions are cell coordinates, like place's.
type myc struct {
	x, y map[string]float64
	rad  map[string]float64 // normalised radius, 0 = centre, 1 = screen edge
	ang  map[string]float64 // radians
	slot map[string]int     // label width available; < 4 means glyph only
}

// seed hashes ids to a stable value in [-1, 1).
func seed(parts ...string) float64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return float64(h.Sum64()>>11)/float64(1<<52) - 1
}

func mycelium(nodes map[string]*gnode, depth map[string]int, w, top, bottom int) myc {
	m := myc{x: map[string]float64{}, y: map[string]float64{}, rad: map[string]float64{},
		ang: map[string]float64{}, slot: map[string]int{}}
	maxD := 0
	rings := map[int][]string{}
	for id, d := range depth {
		maxD = max(maxD, d)
		rings[d] = append(rings[d], id)
	}
	for _, r := range rings {
		sort.Strings(r)
	}
	cx, cy := float64(w)/2, float64(top+bottom)/2
	rx := max(float64(w)/2-8, 4)
	ry := max(float64(bottom-top)/2-2, 2)

	// Rings are spaced to the natural step, or squeezed so the deepest fits.
	base := 0.0
	if len(rings[0]) > 1 {
		base = 0.1
	}
	step := 0.0
	if maxD > 0 {
		step = min(0.3, (1-base)/float64(maxD))
	}
	twoPi := 2 * math.Pi
	norm := func(a float64) float64 { return a - twoPi*math.Floor(a/twoPi) }

	for d := 0; d <= maxD; d++ {
		ring := rings[d]
		rr := base + float64(d)*step
		ang := make([]float64, len(ring))
		for i, id := range ring {
			if d == 0 {
				ang[i] = twoPi * float64(i) / float64(len(ring))
				continue
			}
			sx, sy, k := 0.0, 0.0, 0
			for _, p := range nodes[id].prereqs {
				if pa, ok := m.ang[p]; ok {
					sx += math.Cos(pa)
					sy += math.Sin(pa)
					k++
				}
			}
			mean := 0.0
			if k > 0 {
				mean = math.Atan2(sy, sx)
			}
			ang[i] = norm(mean + seed(id)*0.45)
		}
		if d > 0 {
			relax(ang, rr*(rx+2*ry)/2)
		}
		for i, id := range ring {
			a := norm(ang[i])
			m.ang[id], m.rad[id] = a, rr
			m.x[id] = cx + rr*rx*math.Cos(a)
			m.y[id] = cy + rr*ry*math.Sin(a)
		}
	}

	// A label gets the horizontal room up to its nearest neighbour on the same
	// or an adjacent row; cramped nodes keep only their glyph.
	ids := make([]string, 0, len(m.x))
	for id := range m.x {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, a := range ids {
		room := 18.0
		for _, b := range ids {
			if a != b && math.Abs(m.y[a]-m.y[b]) < 2 {
				room = min(room, math.Abs(m.x[a]-m.x[b]))
			}
		}
		m.slot[a] = int(room) - 1
	}
	return m
}

// relax pushes angles apart on one ring, in sorted order with a fixed number
// of passes, until neighbours keep about a label's width of arc between them.
func relax(ang []float64, rEff float64) {
	n := len(ang)
	if n < 2 {
		return
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return ang[idx[i]] < ang[idx[j]] })
	gap := min(10/math.Max(rEff, 1), 2*math.Pi/float64(n))
	for pass := 0; pass < 40; pass++ {
		moved := false
		for k := 0; k < n; k++ {
			a, b := idx[k], idx[(k+1)%n]
			diff := ang[b] - ang[a]
			if k == n-1 {
				diff += 2 * math.Pi
			}
			if diff < gap-1e-9 {
				push := (gap - diff) / 2
				ang[a] -= push
				ang[b] += push
				moved = true
			}
		}
		if !moved {
			break
		}
	}
}
