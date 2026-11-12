package main

import (
	"fmt"
	"sort"
	"strings"
)

// Condensing: a big plan is mostly finished work, and drawn node by node that
// work buries the part still moving. Finished nodes (done or cancelled) joined
// by dependency edges to other finished nodes form a group; a group of at
// least condenseAt is drawn as one node carrying its members' outside edges.
// Enter on a group opens it, x folds it back, c turns condensing off and on.
// Only the layered view condenses; the radial view keeps every node.

// defaultCondense is --condense's default: on a 255-node plan the finished
// work fell into groups of 94, 41 and 37 and a tail of 4 or fewer, so 8
// condenses the bulk and leaves small loose ends drawn as they are.
const defaultCondense = 8

// watchCondense is --condense, read by watchLoop for every view it runs (the
// picker builds its own views). 0 turns condensing off.
var watchCondense = defaultCondense

const groupPrefix = "group:"

func isGroupID(id string) bool { return strings.HasPrefix(id, groupPrefix) }

func finished(s string) bool { return s == "done" || s == "cancelled" }

// condensed is the graph as the layered view draws it.
type condensed struct {
	nodes   map[string]*gnode // what is drawn: plain nodes and closed groups
	groupOf map[string]string // member id -> its closed group's id
	comp    map[string][]string
}

// components returns every finished component of at least min nodes, keyed by
// each member, members sorted. least < 2 returns none: a group of one is a node.
func components(nodes map[string]*gnode, least int) map[string][]string {
	out := map[string][]string{}
	if least < 2 {
		return out
	}
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for id, g := range nodes {
		if finished(g.status) {
			parent[id] = id
		}
	}
	for id, g := range nodes {
		if !finished(g.status) {
			continue
		}
		for _, p := range g.prereqs {
			if q, ok := nodes[p]; ok && finished(q.status) {
				if a, b := find(id), find(p); a != b {
					parent[a] = b
				}
			}
		}
	}
	by := map[string][]string{}
	for id := range parent {
		r := find(id)
		by[r] = append(by[r], id)
	}
	for _, ms := range by {
		if len(ms) < least {
			continue
		}
		sort.Strings(ms)
		for _, m := range ms {
			out[m] = ms
		}
	}
	return out
}

// groupID names a group by its smallest member id, so it keeps its name (and
// its place) while the members it already has stay in it.
func groupID(members []string) string { return groupPrefix + members[0] }

// groupLabel is the members' most common key prefix (the text before the first
// "-") when at least half share it, else the top two when they cover half,
// after the count.
func groupLabel(nodes map[string]*gnode, members []string) string {
	count := map[string]int{}
	for _, m := range members {
		p, _, _ := strings.Cut(nodes[m].key, "-")
		count[p]++
	}
	ps := make([]string, 0, len(count))
	for p := range count {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool {
		if count[ps[i]] != count[ps[j]] {
			return count[ps[i]] > count[ps[j]]
		}
		return ps[i] < ps[j]
	})
	name := "done"
	switch {
	case len(ps) > 0 && 2*count[ps[0]] >= len(members):
		name = ps[0]
	case len(ps) > 1 && 2*(count[ps[0]]+count[ps[1]]) >= len(members):
		name = ps[0] + "·" + ps[1]
	}
	// Count first: a crowded layer cuts labels to a few cells, and the size
	// is what says this is a group.
	return fmt.Sprintf("×%d %s", len(members), name)
}

// condense returns the drawn graph: each finished component of at least least
// nodes that open does not name becomes one group node.
func condense(nodes map[string]*gnode, least int, open map[string]bool) condensed {
	cd := condensed{nodes: map[string]*gnode{}, groupOf: map[string]string{}, comp: components(nodes, least)}
	groups := map[string][]string{}
	for m, ms := range cd.comp {
		if open[m] {
			continue
		}
		gid := groupID(ms)
		cd.groupOf[m] = gid
		groups[gid] = ms
	}
	if len(groups) == 0 {
		cd.nodes = nodes
		return cd
	}
	at := func(id string) string {
		if gid, ok := cd.groupOf[id]; ok {
			return gid
		}
		return id
	}
	// remap maps ids to what draws them, drops edges inside one group, dedups.
	remap := func(self string, ids []string) []string {
		var out []string
		seen := map[string]bool{}
		for _, id := range ids {
			if _, ok := nodes[id]; !ok {
				continue
			}
			t := at(id)
			if t == self || seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, t)
		}
		return out
	}
	for id, g := range nodes {
		if _, ok := cd.groupOf[id]; ok {
			continue
		}
		cd.nodes[id] = &gnode{id: id, key: g.key, status: g.status, created: g.created,
			prereqs: remap(id, g.prereqs), children: remap(id, g.children)}
	}
	for gid, ms := range groups {
		grp := &gnode{id: gid, key: groupLabel(nodes, ms), status: "done", created: nodes[ms[0]].created, members: len(ms)}
		var pre, ch []string
		for _, m := range ms {
			grp.created = min(grp.created, nodes[m].created)
			pre = append(pre, nodes[m].prereqs...)
			ch = append(ch, nodes[m].children...)
		}
		sort.Strings(pre)
		sort.Strings(ch)
		grp.prereqs, grp.children = remap(gid, pre), remap(gid, ch)
		cd.nodes[gid] = grp
	}
	return cd
}

// node is an id as drawn this frame: a group or plain node from the shown
// graph, else the real node (a member a particle or event still names).
func (v *view) node(id string) *gnode {
	if g := v.shown[id]; g != nil {
		return g
	}
	return v.nodes[id]
}

// drawnAs is the id that draws id this frame: its closed group, or itself.
func (v *view) drawnAs(id string) string {
	if gid, ok := v.cd.groupOf[id]; ok {
		return gid
	}
	return id
}

// recondense rebuilds the shown graph when the graph, a status, the setting or
// an open group changed. A new group starts where its members were, and the
// members of a group just opened start where it was, so both glide.
func (v *view) recondense(sel string) {
	at := v.condenseAt
	if v.condenseOff || v.mycelium {
		at = 0
	}
	// A selection inside a closed group (the needs-you panel goes straight to
	// a node) opens the group, or the selection would point at nothing drawn.
	if _, ok := v.cd.groupOf[sel]; ok {
		for _, m := range v.cd.comp[sel] {
			v.opened[m] = true
		}
		v.gen++
	}
	if v.built == v.gen && v.builtAt == at && v.shown != nil {
		return
	}
	prev := v.cd
	v.cd = condense(v.nodes, at, v.opened)
	v.shown, v.built, v.builtAt = v.cd.nodes, v.gen, at
	for id, g := range v.shown {
		if g.members == 0 {
			if old, ok := prev.groupOf[id]; ok && v.cd.groupOf[id] == "" {
				if gv := v.vis[old]; gv != nil && gv.placed {
					if vn := v.vis[id]; vn != nil {
						vn.x, vn.y = gv.x, gv.y
					}
				}
			}
			continue
		}
		if v.vis[id] == nil {
			vn := &viewNode{grow: 1}
			n := 0.0
			for _, m := range v.cd.comp[g.id[len(groupPrefix):]] {
				if mv := v.vis[m]; mv != nil && mv.placed {
					vn.x, vn.y, n = vn.x+mv.x, vn.y+mv.y, n+1
				}
			}
			if n > 0 {
				vn.x, vn.y, vn.placed = vn.x/n, vn.y/n, true
			}
			v.vis[id] = vn
		}
	}
	for id := range v.vis {
		if isGroupID(id) && v.shown[id] == nil {
			delete(v.vis, id)
		}
	}
	if sig := topoSignature(v.shown); sig != v.shownSig {
		v.shownSig = sig
		v.shownTopo++
	}
}

// groupKey handles the condensing keys and reports whether k was one: enter on
// a group opens it, x folds the selected node's group back, c turns
// condensing off and on (on again folds every open group).
func (v *view) groupKey(k string) bool {
	if k != "enter" && k != "x" && k != "c" {
		return false
	}
	sel := v.selected()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.mycelium {
		return false
	}
	switch k {
	case "enter":
		g := v.shown[sel]
		if g == nil || g.members == 0 {
			return false // a plain node: the inspector opens it
		}
		ms := v.cd.comp[sel[len(groupPrefix):]]
		for _, m := range ms {
			v.opened[m] = true
		}
		v.gen++
		v.selectID(ms[0])
		return true
	case "x":
		ms := v.cd.comp[sel]
		if len(ms) == 0 || !v.opened[sel] {
			return true // not in an open group: nothing to fold
		}
		for _, m := range ms {
			delete(v.opened, m)
		}
		v.gen++
		v.selectID(groupID(ms))
		return true
	default: // "c"
		v.condenseOff = !v.condenseOff
		if !v.condenseOff {
			v.opened = map[string]bool{}
		}
		v.gen++
		v.say(map[bool]string{true: "condensing off: every node drawn", false: "condensing on: finished groups folded"}[v.condenseOff], rgb{90, 210, 170})
		return true
	}
}

// selectID moves the inspector's selection to id.
func (v *view) selectID(id string) {
	if v.insp == nil {
		return
	}
	v.insp.mu.Lock()
	v.insp.sel = id
	v.insp.mu.Unlock()
}
