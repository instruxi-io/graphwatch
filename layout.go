package main

import "sort"

// gnode is one node as the view knows it. Prereqs are the nodes it depends on:
// stored edges point FROM the dependent TO its prerequisite (a `requires` edge reads
// "dependent needs prerequisite"), and the
// view draws them the other way round: a prerequisite sits ABOVE what it
// unblocks, so work falls down the screen and the DAG fans out as it goes.
type gnode struct {
	id, key, status string
	created         int // arrival order, the stable tiebreak
	prereqs         []string
	children        []string
}

// layers assigns each node its depth: 0 for a node with no prerequisites,
// otherwise one below its deepest prerequisite. A cycle (only possible in a
// graph-mode graph) is cut where it is found rather than looping.
func layers(nodes map[string]*gnode) map[string]int {
	depth := make(map[string]int, len(nodes))
	visiting := map[string]bool{}
	var walk func(id string) int
	walk = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if visiting[id] {
			return 0
		}
		visiting[id] = true
		d := 0
		for _, p := range nodes[id].prereqs {
			if _, ok := nodes[p]; ok {
				d = max(d, walk(p)+1)
			}
		}
		visiting[id] = false
		depth[id] = d
		return d
	}
	for id := range nodes {
		walk(id)
	}
	return depth
}

// order arranges each layer to reduce crossings: the Sugiyama framework's
// barycenter heuristic, alternating downward sweeps (order by the mean position
// of a node's prerequisites) and upward sweeps (by its children). prev seeds
// the order so a growing graph does not reshuffle every frame.
func order(nodes map[string]*gnode, depth map[string]int, prev map[string]float64) [][]string {
	n := 0
	for _, d := range depth {
		n = max(n, d+1)
	}
	rows := make([][]string, n)
	for id, d := range depth {
		rows[d] = append(rows[d], id)
	}
	for _, row := range rows {
		sort.Slice(row, func(i, j int) bool {
			a, b := nodes[row[i]], nodes[row[j]]
			pa, oka := prev[a.id]
			pb, okb := prev[b.id]
			if oka && okb && pa != pb {
				return pa < pb
			}
			if oka != okb {
				return oka // known nodes keep their place; newcomers go right
			}
			if a.created != b.created {
				return a.created < b.created
			}
			// A batch import gives every node one created time and a cluster
			// gives its members one prev position; without a final key the
			// unstable sort over map-ordered rows reshuffled them every frame.
			return a.id < b.id
		})
	}

	pos := map[string]float64{}
	index := func() {
		for _, row := range rows {
			for i, id := range row {
				pos[id] = (float64(i) + 0.5) / float64(len(row))
			}
		}
	}
	index()
	bary := func(row []string, neighbours func(*gnode) []string) {
		key := make(map[string]float64, len(row))
		for _, id := range row {
			sum, k := 0.0, 0
			for _, nb := range neighbours(nodes[id]) {
				if p, ok := pos[nb]; ok {
					sum += p
					k++
				}
			}
			if k == 0 {
				key[id] = pos[id]
			} else {
				key[id] = sum / float64(k)
			}
		}
		sort.SliceStable(row, func(i, j int) bool { return key[row[i]] < key[row[j]] })
	}
	for sweep := 0; sweep < 4; sweep++ {
		for d := 1; d < len(rows); d++ {
			bary(rows[d], func(g *gnode) []string { return g.prereqs })
			index()
		}
		for d := len(rows) - 2; d >= 0; d-- {
			bary(rows[d], func(g *gnode) []string { return g.children })
			index()
		}
	}
	return rows
}

// placement maps layers to target cell positions. Rows are spaced to fill the
// height; when the graph is deeper than the screen, the view scrolls to keep
// the busiest layer a third of the way down.
type placement struct {
	x, y   map[string]float64 // cell coordinates of each node's glyph
	rowGap int
	slot   map[string]int // label width available to each node
}

func place(rows [][]string, w, top, bottom int, focus int) placement {
	p := placement{x: map[string]float64{}, y: map[string]float64{}, slot: map[string]int{}}
	avail := bottom - top
	n := max(len(rows), 1)
	p.rowGap = 6
	if n > 1 {
		p.rowGap = min(6, max(3, avail/n))
	}
	offset := 0
	if span := (n - 1) * p.rowGap; span > avail-1 {
		offset = focus*p.rowGap - avail/3
		offset = max(0, min(offset, span-(avail-2)))
	}
	for d, row := range rows {
		// Capped and centred, so a narrow layer stays under its parents and
		// the curves between layers stay steep rather than smeared flat.
		k := len(row)
		slotW := min(24, float64(w-4)/float64(max(k, 1)))
		left := float64(w)/2 - slotW*float64(k)/2
		for i, id := range row {
			p.x[id] = left + slotW*(float64(i)+0.5)
			p.y[id] = float64(top + d*p.rowGap - offset)
			p.slot[id] = int(slotW) - 1
		}
	}
	return p
}
