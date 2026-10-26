package main

import (
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"
)

// Mycelium growth: the hyphae are grown, not drawn. Every edge prerequisite ->
// dependent is a tip that starts at the prerequisite and is steered toward the
// dependent; every node also germinates a few short tips of its own. Growth
// follows the Neighbour-Sensing model (Meskauskas, Fricker and Moore): a tip
// has a position and a heading that turns gradually (persistence), and each
// step it advances one braille sub-pixel. Three things steer it:
//
//   - attraction toward its target, through a few jittered waypoints, as in
//     space colonization (Runions et al. 2007);
//   - negative autotropism: an occupancy field of every hypha grown so far,
//     which a tip turns away from (inconvergent's hyphae avoid collisions the
//     same way), so filaments spread into a mat instead of re-tracing;
//   - a small seeded wobble.
//
// A growing tip spawns lateral branches 30-70 degrees off its heading when its
// neighbourhood is sparse; branches are shorter-lived, die on a step budget or
// on touching another hypha (they fuse, as real hyphae anastomose), and may
// branch again. Those branches, not the node-to-node paths, are what make it
// read as mycelium.
//
// Every random draw comes from a PRNG seeded by the ids involved, and the
// whole structure is grown once per graph or screen change and cached: frames
// only choose how much of it to reveal and in what colour.

// Growth bounds. A 100-node graph on a large terminal stays a few
// milliseconds of work, once.
const (
	maxLateral     = 6    // first-order lateral branches per edge
	maxFamily      = 14   // all branches (any order) descending from one edge
	maxGermFamily  = 30   // all branches descending from one node's germ tubes
	maxGeneration  = 3    // a branch of a branch of a branch, no deeper
	budgetFraction = 0.24 // of the sub-pixels in the growth area
	budgetCap      = 16000
	maxSteps       = 900
)

// hdot is one grown sub-pixel. step is the global growth step it appeared at,
// which is the order a reveal plays it back in.
type hdot struct {
	x, y int16
	step uint16
	kind uint8 // kindPrimary, kindLateral, kindGerm
	gen  uint8
}

const (
	kindPrimary = iota
	kindLateral
	kindGerm
)

// grown is the cached structure for one graph at one screen size.
type grown struct {
	owner    map[string][]hdot     // by the node whose state reveals them, in step order
	maxStep  map[string]int        // last step among a node's dots
	path     map[[2]string][]int32 // primary filament prerequisite -> dependent, packed y<<16|x
	dots     int
	budget   int
	branches int
	edges    int
}

type family struct {
	rng      *rand.Rand
	limit    int // maxFamily or maxGermFamily
	owner    string
	to       string // the dependent, for an edge's family
	lateral  int
	branches int
	path     *[]int32
}

type tip struct {
	x, y, hx, hy float64
	fam          *family
	id, parent   int32
	kind, gen    uint8
	life, born   int
	way          [][2]float64
	wi           int
	last         int
	dead         bool
}

func seedRNG(parts ...string) *rand.Rand {
	h1, h2 := fnv.New64a(), fnv.New64()
	for _, p := range parts {
		h1.Write([]byte(p))
		h1.Write([]byte{0})
		h2.Write([]byte(p))
		h2.Write([]byte{1})
	}
	return rand.New(rand.NewPCG(h1.Sum64(), h2.Sum64()))
}

// grow builds the structure for nodes at the given cell positions. Pixel
// space is 2w x 4h; growth is confined to the rows between top and bottom.
func grow(nodes map[string]*gnode, depth map[string]int, pos placement, w, top, bottom int) *grown {
	W, H := w*2, bottom*4
	y0 := top * 4
	g := &grown{owner: map[string][]hdot{}, maxStep: map[string]int{}, path: map[[2]string][]int32{}}
	if W <= 0 || H <= y0 {
		return g
	}
	occ := make([]int32, W*H)
	area := W * (H - y0)
	g.budget = min(budgetCap, int(float64(area)*budgetFraction))
	reserve := g.budget * 85 / 100 // branches stop here; primaries may use the rest

	ids := make([]string, 0, len(nodes))
	maxD := 0
	for id := range nodes {
		ids = append(ids, id)
		maxD = max(maxD, depth[id])
	}
	sort.Strings(ids)
	centre := func(id string) (float64, float64) { return pos.x[id]*2 + 1, pos.y[id]*4 + 2 }

	var tips []*tip
	nextID := int32(1)
	add := func(t *tip) {
		t.id, t.last = nextID, -1
		nextID++
		tips = append(tips, t)
	}

	// One primary tip per edge, steered through jittered waypoints.
	for _, id := range ids {
		n := nodes[id]
		pre := append([]string(nil), n.prereqs...)
		sort.Strings(pre)
		for _, p := range pre {
			x0, y0p := centre(p)
			x1, y1 := centre(id)
			dx, dy := x1-x0, y1-y0p
			l := math.Hypot(dx, dy)
			if l < 1 {
				continue
			}
			g.edges++
			ux, uy := dx/l, dy/l
			fam := &family{rng: seedRNG("edge", p, id), limit: maxFamily, owner: p, to: id}
			var path []int32
			fam.path = &path
			k := int(math.Max(1, math.Min(4, l/16)))
			var way [][2]float64
			for i := 1; i <= k; i++ {
				t := float64(i) / float64(k+1)
				off := l * 0.14 * (fam.rng.Float64()*2 - 1)
				way = append(way, [2]float64{x0 + dx*t - uy*off, y0p + dy*t + ux*off})
			}
			way = append(way, [2]float64{x1, y1})
			a := math.Atan2(way[0][1]-y0p, way[0][0]-x0) + (fam.rng.Float64()*2-1)*0.5
			add(&tip{x: x0 + ux*2, y: y0p + uy*2, hx: math.Cos(a), hy: math.Sin(a), fam: fam,
				kind: kindPrimary, life: int(l*3) + 20, way: way})
		}
	}
	// Germ tubes: every node sprouts a few short tips of its own, more and
	// longer the older (shallower) it is, so a mat forms around old nodes.
	for _, id := range ids {
		n := nodes[id]
		fam := &family{rng: seedRNG("germ", id), limit: maxGermFamily, owner: id}
		age := float64(maxD-depth[id]+1) / float64(maxD+1)
		count := 3 + int(age*5) + min(len(n.children)+len(n.prereqs), 4)
		base := fam.rng.Float64() * 2 * math.Pi
		x, y := centre(id)
		for i := 0; i < count; i++ {
			a := base + 2*math.Pi*float64(i)/float64(count) + (fam.rng.Float64()*2-1)*0.4
			add(&tip{x: x + 3*math.Cos(a), y: y + 3*math.Sin(a), hx: math.Cos(a), hy: math.Sin(a), fam: fam,
				kind: kindGerm, life: int(8 + age*22 + fam.rng.Float64()*12)})
		}
	}

	// The colony grows outward: branches and germ tubes lean away from the
	// middle of the growth area, where the oldest nodes sit.
	ocx, ocy := float64(W)/2, float64(y0+H)/2
	pix := func(x, y float64) int {
		xi, yi := int(math.Floor(x+0.5)), int(math.Floor(y+0.5))
		if xi < 0 || xi >= W || yi < y0 || yi >= H {
			return -1
		}
		return yi*W + xi
	}

	for step := 0; step < maxSteps; step++ {
		alive := false
		for ti := 0; ti < len(tips); ti++ { // tips appended this step start next step
			t := tips[ti]
			if t.dead {
				continue
			}
			alive = true
			rng := t.fam.rng

			// Attraction.
			ax, ay := t.hx, t.hy
			attr := 0.0
			if t.kind == kindPrimary {
				wp := t.way[t.wi]
				dx, dy := wp[0]-t.x, wp[1]-t.y
				d := math.Hypot(dx, dy)
				if t.wi < len(t.way)-1 && d < 4 {
					t.wi++
					wp = t.way[t.wi]
					dx, dy = wp[0]-t.x, wp[1]-t.y
					d = math.Hypot(dx, dy)
				}
				if t.wi == len(t.way)-1 && d < 1.6 {
					t.dead = true
					continue
				}
				if d > 0 {
					ax, ay = dx/d, dy/d
				}
				attr = 0.22 + 0.5*math.Min(1, float64(t.born)/float64(t.life))
				if t.wi == len(t.way)-1 && d < 8 {
					attr = 0.9
				}
			}

			// Negative autotropism: turn away from other hyphae nearby.
			vx, vy, dens := 0.0, 0.0, 0
			const R = 3
			cx, cy := int(t.x+0.5), int(t.y+0.5)
			for oy := -R; oy <= R; oy++ {
				for ox := -R; ox <= R; ox++ {
					qx, qy := cx+ox, cy+oy
					if (ox == 0 && oy == 0) || qx < 0 || qx >= W || qy < y0 || qy >= H {
						continue
					}
					o := occ[qy*W+qx]
					if o == 0 || o == t.id || (o == t.parent && t.born < 6) {
						continue
					}
					dens++
					d2 := float64(ox*ox + oy*oy)
					vx -= float64(ox) / d2
					vy -= float64(oy) / d2
				}
			}
			avoid := 0.35
			if t.kind != kindPrimary {
				avoid = 0.7
			} else if attr >= 0.9 {
				avoid = 0.05
			}
			persist := 0.86
			if t.kind == kindPrimary {
				persist = 1 - attr
			} else if ox, oy := t.x-ocx, (t.y-ocy)*1.4; ox != 0 || oy != 0 {
				d := math.Hypot(ox, oy)
				ax, ay, attr = ox/d, oy/d, 0.12
			}
			nx := t.hx*persist + ax*attr + vx*avoid*0.25
			ny := t.hy*persist + ay*attr + vy*avoid*0.25
			wob := (rng.Float64()*2 - 1) * 0.28
			if t.kind != kindPrimary {
				wob *= 1.3
			}
			s, c := math.Sincos(math.Atan2(ny, nx) + wob)
			// The heading turns at most ~0.5 rad a step: it never jumps.
			cur := math.Atan2(t.hy, t.hx)
			turn := math.Remainder(math.Atan2(s, c)-cur, 2*math.Pi)
			turn = math.Max(-0.5, math.Min(0.5, turn))
			t.hy, t.hx = math.Sincos(cur + turn)
			t.x += t.hx
			t.y += t.hy
			t.born++

			p := pix(t.x, t.y)
			if p < 0 {
				t.dead = true
				continue
			}
			if p != t.last {
				o := occ[p]
				fused := o != 0 && o != t.id && !(o == t.parent && t.born < 6)
				if (t.kind != kindPrimary && g.dots >= reserve) || g.dots >= g.budget {
					t.dead = true
					continue
				}
				occ[p] = t.id
				t.last = p
				g.dots++
				g.owner[t.fam.owner] = append(g.owner[t.fam.owner], hdot{x: int16(p % W), y: int16(p / W),
					step: uint16(step), kind: t.kind, gen: t.gen})
				if t.kind == kindPrimary {
					*t.fam.path = append(*t.fam.path, int32((p/W)<<16|(p%W)))
				}
				if fused && t.kind != kindPrimary {
					t.dead = true // touched another hypha: fuse and stop
					continue
				}
			}
			if t.kind != kindPrimary && t.born >= t.life {
				t.dead = true
				continue
			}

			// Lateral branching where the neighbourhood is sparse.
			pb := 0.12
			if t.kind != kindPrimary {
				pb = 0.11 / float64(t.gen+1)
			}
			f := t.fam
			if t.born > 3 && dens < 5 && t.gen < maxGeneration && f.branches < f.limit &&
				(t.kind != kindPrimary || f.lateral < maxLateral) && g.dots < reserve && rng.Float64() < pb {
				side := 1.0
				if rng.Float64() < 0.5 {
					side = -1
				}
				a := math.Atan2(t.hy, t.hx) + side*(30+rng.Float64()*40)*math.Pi/180
				life := int((8 + rng.Float64()*18) * math.Pow(0.7, float64(t.gen)))
				if t.kind == kindPrimary {
					f.lateral++
				}
				f.branches++
				g.branches++
				kind := uint8(kindLateral)
				if t.kind == kindGerm {
					kind = kindGerm
				}
				add(&tip{x: t.x, y: t.y, hx: math.Cos(a), hy: math.Sin(a), fam: f, parent: t.id,
					kind: kind, gen: t.gen + 1, life: life})
			}
		}
		if !alive {
			break
		}
	}
	for _, t := range tips {
		if t.kind == kindPrimary && t.gen == 0 {
			g.path[[2]string{t.fam.owner, t.fam.to}] = *t.fam.path
		}
	}
	for id, ds := range g.owner {
		g.maxStep[id] = int(ds[len(ds)-1].step)
	}
	return g
}
