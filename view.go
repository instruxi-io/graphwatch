package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"
)

// Heat is how busy something is right now. An event sets it to 1 and it cools
// with this time constant; a running node never cools below a pulse.
const cool = 2.2 // seconds

type viewNode struct {
	x, y     float64 // animated cell position
	placed   bool
	heat     float64
	phase    float64
	lastEmit time.Time
	flared   bool    // the finale wave has reached it
	grow     float64 // mycelium: how far its hyphae have been revealed, 0..1
	doneRank int     // mycelium: completion order (0 = done before we watched); older is dimmer
}

// particle is a pulse of work travelling down an edge, prerequisite to
// dependent: into a node when it starts, out of it when it finishes.
type particle struct {
	from, to string
	t, speed float64
	heat     float64
}

type tick struct {
	at   time.Time
	text string
	col  rgb
}

type view struct {
	mu      sync.Mutex
	title   string
	edgeT   string
	nodes   map[string]*gnode
	vis     map[string]*viewNode
	byKey   map[string]string
	parts   []particle
	ticker  []tick
	link    string // live | polling | reconnecting | connecting
	seq     int64
	events  int
	arrived int
	finale  time.Time
	prevX   map[string]float64
	runNode map[string]string // run id -> node key, learned from run.started
	rng     *rand.Rand
	started time.Time
	// continuous keeps running nodes pulsing and streaming particles every
	// frame. Off (the default), motion follows events and a quiet graph
	// settles to a static frame, which the screen then sends nothing for.
	continuous bool
	clock      func() time.Time // time.Now; tests advance it per frame
	// mycelium switches the layout from layers to the radial growth view.
	mycelium bool
	topo     int // bumped by apply; keys the cached mycelium layout
	mycKey   [3]int
	myc      myc
	grown    *grown // the hyphae, grown once per mycKey
	topoSig  string // nodes and dependency edges; topo moves only when it does
	doneSeq  int
	insp     *inspector // the node inspector; nil draws nothing
	ov       *overlays  // plan-level header; nil keeps the plain header
}

func newView() *view {
	return &view{nodes: map[string]*gnode{}, vis: map[string]*viewNode{}, byKey: map[string]string{},
		prevX: map[string]float64{}, runNode: map[string]string{}, link: "connecting", edgeT: "requires",
		rng: rand.New(rand.NewPCG(1, 2)), started: time.Now(), clock: time.Now}
}

func (v *view) setLink(s string) { v.mu.Lock(); v.link = s; v.mu.Unlock() }

func (v *view) completed() (time.Time, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.finale, !v.finale.IsZero()
}

// apply replaces the graph with a fresh read, turning every status change
// since the last one into heat and particles.
func (v *view) apply(ns []apiNode, es []apiEdge) {
	v.mu.Lock()
	defer v.mu.Unlock()
	next := make(map[string]*gnode, len(ns))
	for _, n := range ns {
		g := &gnode{id: n.ID, key: n.Key, status: n.Status}
		if old, ok := v.nodes[n.ID]; ok {
			g.created = old.created
		} else {
			v.arrived++
			g.created = v.arrived
		}
		next[n.ID] = g
	}
	for _, e := range es {
		if e.Type != v.edgeT {
			continue
		}
		dep, pre := next[e.FromNodeID], next[e.ToNodeID]
		if dep == nil || pre == nil {
			continue
		}
		dep.prereqs = append(dep.prereqs, pre.id)
		pre.children = append(pre.children, dep.id)
	}
	old := v.nodes
	if sig := topoSignature(next); sig != v.topoSig {
		v.topoSig = sig
		v.topo++
	}
	v.nodes = next
	v.byKey = map[string]string{}
	for id, g := range next {
		v.byKey[g.key] = id
		vn, ok := v.vis[id]
		if !ok {
			vn = &viewNode{heat: 1, grow: 1, phase: v.rng.Float64() * 2 * math.Pi}
			v.vis[id] = vn
		}
		if o, ok := old[id]; ok && o.status != g.status {
			v.transition(g, o.status)
		}
	}
	for id := range v.vis {
		if _, ok := next[id]; !ok {
			delete(v.vis, id)
		}
	}
	// A plan that completed and then grew is not complete any more: the
	// graph.completed event was true when it was sent, and new work undid it.
	if !v.finale.IsZero() {
		for _, g := range next {
			if g.status != "done" && g.status != "cancelled" {
				v.finale = time.Time{}
				for _, vn := range v.vis {
					vn.flared = false
				}
				break
			}
		}
	}
}

func (v *view) transition(g *gnode, from string) {
	vn := v.vis[g.id]
	vn.heat = 1
	switch g.status {
	case "running":
		for _, p := range g.prereqs {
			v.parts = append(v.parts, particle{from: p, to: g.id, speed: 0.9 + v.rng.Float64()*0.5, heat: 1})
		}
	case "done":
		vn.grow = 0 // mycelium: the hyphae are revealed once, in growth order
		v.doneSeq++
		vn.doneRank = v.doneSeq
		for _, c := range g.children {
			v.parts = append(v.parts, particle{from: g.id, to: c, speed: 0.7 + v.rng.Float64()*0.4, heat: 0.95})
		}
	}
}

// event records one stream frame: the ticker, the link state, and a jolt of
// heat on the node it names when the frame says which one.
func (v *view) event(ev sseEvent) {
	v.mu.Lock()
	defer v.mu.Unlock()
	switch ev.Event {
	case ":":
		return
	case "ready":
		var d struct{ Listener string }
		_ = json.Unmarshal(ev.Data, &d)
		v.link = d.Listener
		return
	case "closed", "reset":
		v.say(ev.Event+" "+string(ev.Data), rgb{250, 200, 80})
		return
	}
	var row streamRow
	if json.Unmarshal(ev.Data, &row) != nil {
		return
	}
	v.events++
	v.seq = max(v.seq, row.Seq)
	name := ""
	id := ""
	if row.ResourceID != nil {
		if _, ok := v.nodes[*row.ResourceID]; ok {
			id = *row.ResourceID
		}
	}
	for _, k := range []string{"node_id"} {
		if s := row.Metadata[k]; s != "" {
			if _, ok := v.nodes[s]; ok {
				id = s
			}
		}
	}
	for _, k := range []string{"node", "key"} {
		if s := row.Metadata[k]; s != "" {
			if nid, ok := v.byKey[s]; ok {
				id = nid
			}
			name = s
		}
	}
	if v.insp != nil && id != "" {
		v.insp.notify(id) // the hook point: every event for a node refetches its panel
	}
	// A run row names its node only on run.started; remember it, so the
	// finish can say what finished.
	if row.ResourceID != nil && strings.HasPrefix(row.Verb, "run.") {
		if name != "" {
			v.runNode[*row.ResourceID] = name
		} else if k, ok := v.runNode[*row.ResourceID]; ok {
			name = k
			if nid, ok := v.byKey[k]; ok && id == "" {
				id = nid
			}
		}
	}
	if id != "" {
		v.vis[id].heat = max(v.vis[id].heat, 0.8)
		name = v.nodes[id].key
	}
	if row.Verb == "run.judged" && row.Metadata["state"] == "skipped" {
		return // judgment is off: heat, but not worth a ticker line
	}
	col := rgb{150, 160, 190}
	switch {
	case strings.HasPrefix(row.Verb, "run.started"):
		col = rgb{252, 186, 30}
	case row.Verb == "run.finished" && row.Metadata["status"] == "failed":
		col = rgb{240, 80, 64}
	case row.Verb == "run.finished":
		col = rgb{90, 210, 170}
	case row.Verb == "graph.completed":
		col = rgb{255, 250, 190}
		v.finale = v.clock()
	case strings.HasPrefix(row.Verb, "observation."):
		col = rgb{170, 140, 250}
	}
	text := row.Verb
	if name != "" {
		text += "  " + name
	}
	if s := row.Metadata["status"]; s != "" {
		text += "  " + s
	}
	v.say(text, col)
}

func (v *view) say(text string, col rgb) {
	v.ticker = append(v.ticker, tick{at: v.clock(), text: text, col: col})
	if len(v.ticker) > 8 {
		v.ticker = v.ticker[len(v.ticker)-8:]
	}
}

func (v *view) ready(g *gnode) bool {
	for _, p := range g.prereqs {
		if s := v.nodes[p].status; s != "done" && s != "cancelled" {
			return false
		}
	}
	return true
}

// look is a node's glyph and colour: the shading of the dot IS the state.
func (v *view) look(g *gnode, vn *viewNode, now float64) (string, rgb, bool) {
	h := vn.heat
	switch g.status {
	case "done":
		return "●", rgb{64, 196, 160}.mix(thermal(0.75+0.25*h), h*0.85), h > 0.5
	case "running":
		// One step a second, every spinner on the same beat, so a plan with
		// many running nodes repaints once a second rather than on as many
		// frames as it has spinners.
		rate, phase := 1.0, 0.0
		if v.continuous {
			rate, phase = 7, vn.phase
		}
		spin := []string{"◐", "◓", "◑", "◒"}[int(now*rate+phase)%4]
		return spin, thermal(0.58 + 0.42*h), true
	case "failed":
		return "◉", rgb{235, 64, 52}.mix(rgb{255, 230, 200}, h*0.6), true
	case "cancelled":
		return "⊘", rgb{90, 92, 104}, false
	case "needs_review":
		// Held for a person: static (not animated), so it never repaints.
		return "◈", rgb{250, 190, 60}, false
	}
	if v.ready(g) {
		return "◎", rgb{200, 206, 222}.mix(thermal(0.9), h*0.6), false
	}
	if v.mycelium {
		return "∙", rgb{78, 84, 104}.mix(thermal(0.8), h*0.7), false // a faint spore
	}
	return "○", rgb{92, 98, 118}.mix(thermal(0.8), h*0.7), false
}

// edgeLook colours an edge by what it is carrying.
func (v *view) edgeLook(from, to *gnode) (rgb, float64, int) {
	switch {
	case to.status == "running":
		return thermal(0.5), 0.5, 1 // work being fed in: solid and warm
	case from.status == "done" && to.status == "done":
		return rgb{40, 100, 90}, 0.2, 2 // settled: cool and dotted
	case from.status == "done":
		return rgb{120, 126, 150}, 0.3, 2 // released, waiting to be taken
	}
	return rgb{58, 60, 78}, 0.1, 3
}

func bezier(x0, y0, x1, y1, t float64) (float64, float64) {
	dy := y1 - y0
	c1x, c1y, c2x, c2y := x0, y0+dy*0.5, x1, y1-dy*0.5
	u := 1 - t
	x := u*u*u*x0 + 3*u*u*t*c1x + 3*u*t*t*c2x + t*t*t*x1
	y := u*u*u*y0 + 3*u*u*t*c1y + 3*u*t*t*c2y + t*t*t*y1
	return x, y
}

// frame advances the animation by dt and draws it at w×h cells.
func (v *view) frame(w, h int, dt float64) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	wall := v.clock()
	now := wall.Sub(v.started).Seconds()
	c := newCanvas(w, h)

	depth := layers(v.nodes)
	var pl placement
	if v.mycelium {
		// The layout depends only on the graph and the screen size, so it is
		// computed when either changes and is the same object every frame.
		if key := [3]int{v.topo, w, h}; v.myc.x == nil || key != v.mycKey {
			v.myc, v.mycKey = mycelium(v.nodes, depth, w, 4, h-6), key
			pl = placement{x: v.myc.x, y: v.myc.y, slot: v.myc.slot}
			v.grown = grow(v.nodes, depth, pl, w, 3, h-5)
		}
		pl = placement{x: v.myc.x, y: v.myc.y, slot: v.myc.slot}
	} else {
		rows := order(v.nodes, depth, v.prevX)
		focus := 0
		for d, row := range rows {
			busy := false
			for _, id := range row {
				if s := v.nodes[id].status; s == "running" || (s != "done" && s != "cancelled" && v.ready(v.nodes[id])) {
					busy = true
				}
			}
			if busy {
				focus = d
				break
			}
		}
		pl = place(rows, w, 4, h-6, focus)
		for id, x := range pl.x {
			v.prevX[id] = x
		}
	}

	// Animate: glide to target, cool down, keep running nodes pulsing and
	// feeding particles down their incoming edges.
	glide := 1 - math.Exp(-dt*5)
	// Draw in a fixed order: overlapping labels and tied edge dots are won by
	// whoever draws last, and map order changes every frame.
	ids := make([]string, 0, len(v.nodes))
	for id := range v.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		g := v.nodes[id]
		vn := v.vis[id]
		tx, ty := pl.x[id], pl.y[id]
		if !vn.placed {
			vn.x, vn.y = tx, ty
			for _, p := range g.prereqs { // born out of its prerequisite
				if pv := v.vis[p]; pv != nil && pv.placed {
					vn.x, vn.y = pv.x, pv.y
					break
				}
			}
			vn.placed = true
		}
		vn.x += (tx - vn.x) * glide
		vn.y += (ty - vn.y) * glide
		if v.mycelium { // the hyphae were grown to the target: sit on their ends
			vn.x, vn.y = tx, ty
		}
		vn.heat *= math.Exp(-dt / cool)
		vn.grow = min(1, vn.grow+dt/revealSeconds)
		if g.status == "running" && !v.continuous {
			vn.heat = max(vn.heat, 0.55) // a steady glow; transitions still flare it
		}
		if g.status == "running" && v.continuous {
			vn.heat = max(vn.heat, 0.45+0.2*math.Sin(now*4+vn.phase))
			if len(g.prereqs) > 0 && wall.Sub(vn.lastEmit) > 700*time.Millisecond {
				vn.lastEmit = wall
				p := g.prereqs[v.rng.IntN(len(g.prereqs))]
				v.parts = append(v.parts, particle{from: p, to: id, speed: 0.6 + v.rng.Float64()*0.3, heat: 0.7})
			}
		}
		if !v.finale.IsZero() && !vn.flared && wall.Sub(v.finale) > time.Duration(depth[id])*140*time.Millisecond {
			vn.flared, vn.heat = true, 1
			for _, ch := range g.children {
				v.parts = append(v.parts, particle{from: id, to: ch, speed: 1.4, heat: 1})
			}
		}
	}

	px := func(id string) (float64, float64) {
		vn := v.vis[id]
		return vn.x*2 + 1, vn.y*4 + 2
	}

	if v.mycelium {
		v.drawGrowth(c, ids, now)
	}
	// Edges: prerequisite above, dependent below.
	for _, id := range ids {
		if v.mycelium {
			break
		}
		g := v.nodes[id]
		for _, p := range g.prereqs {
			x0, y0 := px(p)
			x1, y1 := px(id)
			y0, y1 = y0+6, y1-3 // below the label, into the top of the dot
			col, pri, stride := v.edgeLook(v.nodes[p], g)
			n := int(math.Hypot(x1-x0, y1-y0)*1.4) + 2
			for i := 0; i <= n; i++ {
				if i%stride != 0 {
					continue
				}
				x, y := bezier(x0, y0, x1, y1, float64(i)/float64(n))
				c.dot(int(x+0.5), int(y+0.5), col, pri)
			}
		}
	}

	// Particles, each with a short cooling trail.
	live := v.parts[:0]
	for _, pt := range v.parts {
		pt.t += pt.speed * dt
		if pt.t >= 1 || v.vis[pt.from] == nil || v.vis[pt.to] == nil {
			if pt.t >= 1 {
				if vn := v.vis[pt.to]; vn != nil {
					vn.heat = max(vn.heat, pt.heat*0.6)
				}
			}
			continue
		}
		x0, y0 := px(pt.from)
		x1, y1 := px(pt.to)
		var path []int32
		if v.mycelium {
			path = v.grown.path[[2]string{pt.from, pt.to}]
		} else {
			y0, y1 = y0+6, y1-3
		}
		for k := 0; k < 5; k++ {
			t := pt.t - float64(k)*0.035
			if t < 0 {
				break
			}
			var x, y float64
			if v.mycelium {
				if len(path) == 0 {
					break
				}
				q := path[min(len(path)-1, int(t*float64(len(path))))]
				x, y = float64(q&0xffff), float64(q>>16)
			} else {
				x, y = bezier(x0, y0, x1, y1, t)
			}
			heat := pt.heat * (1 - float64(k)*0.18)
			c.dot(int(x+0.5), int(y+0.5), thermal(heat), 1+heat)
		}
		live = append(live, pt)
	}
	v.parts = live

	// Halos, then the dots themselves and their labels.
	for _, id := range ids {
		vn := v.vis[id]
		if vn.heat < 0.12 || v.mycelium {
			continue
		}
		cx, cy := px(id)
		r := 3 + vn.heat*3.5
		col := thermal(vn.heat * 0.85).scale(0.35 + 0.65*vn.heat)
		for a := 0; a < 20; a++ {
			th := float64(a) / 20 * 2 * math.Pi
			c.dot(int(cx+r*math.Cos(th)+0.5), int(cy+r*0.9*math.Sin(th)+0.5), col, vn.heat*0.9)
		}
	}
	counts := map[string]int{}
	for _, id := range ids {
		g := v.nodes[id]
		vn := v.vis[id]
		glyph, col, bold := v.look(g, vn, now)
		cx, cy := int(vn.x+0.5), int(vn.y+0.5)
		c.put(cx, cy, glyph, col, bold)
		labelCol := rgb{120, 126, 146}.mix(col, 0.35+0.5*vn.heat)
		if !v.mycelium || pl.slot[id] >= 4 { // a cramped mycelium node keeps only its glyph
			c.putCentered(cx, cy+1, g.key, min(pl.slot[id], 18), labelCol, vn.heat > 0.6)
		}
		state := g.status
		if state != "done" && state != "running" && state != "failed" && state != "cancelled" && state != "needs_review" {
			if v.ready(g) {
				state = "ready"
			} else {
				state = "waiting"
			}
		}
		counts[state]++
	}

	// Chrome: title, link state and counts on top; the event ticker below.
	dim := rgb{110, 116, 136}
	c.put(1, 0, "◉ graphwatch", thermal(0.85), true)
	c.put(15, 0, v.title, rgb{210, 214, 228}, true)
	linkCol := rgb{90, 210, 170}
	if v.link != "live" {
		linkCol = rgb{250, 190, 60}
	}
	right := fmt.Sprintf("%s · seq %d · %d events", v.link, v.seq, v.events)
	c.put(w-len([]rune(right))-2, 0, right, linkCol, false)
	x := 1
	plain := v.ov == nil
	if !plain {
		line := headerLine(v.ov.snapshot(), w-2)
		c.put(1, 1, line, rgb{200, 206, 222}, false)
		if i := strings.Index(line, "HELD "); i >= 0 {
			badge := line[i:]
			if j := strings.Index(badge, " "); j >= 0 {
				if k := strings.Index(badge[j+1:], " "); k >= 0 {
					badge = badge[:j+1+k]
				}
			}
			c.put(1+len([]rune(line[:i])), 1, badge, rgb{235, 64, 52}, true)
		}
	}
	for _, s := range []struct {
		k, g string
		col  rgb
	}{
		{"done", "●", rgb{64, 196, 160}}, {"running", "◐", thermal(0.8)}, {"ready", "◎", rgb{200, 206, 222}},
		{"waiting", "○", rgb{120, 126, 146}}, {"failed", "◉", rgb{235, 64, 52}}, {"needs_review", "◈", rgb{250, 190, 60}},
	} {
		if !plain {
			break
		}
		label := fmt.Sprintf("%s %d %s", s.g, counts[s.k], s.k)
		c.put(x, 1, label, s.col, false)
		x += len([]rune(label)) + 3
	}
	if !v.finale.IsZero() {
		c.putCentered(w/2, 2, "✦ plan complete ✦", w, thermal(0.9+0.1*math.Sin(now*6)), true)
	}
	for i, t := range v.ticker[max(0, len(v.ticker)-4):] {
		row := h - 4 + i
		age := wall.Sub(t.at).Seconds()
		fade := clamp01(1 - age/12)
		c.put(2, row, t.at.Format("15:04:05"), dim.scale(0.5+0.5*fade), false)
		c.put(12, row, t.text, rgb{70, 72, 90}.mix(t.col, 0.3+0.7*fade), age < 1.5)
	}

	v.drawInspector(c, w, h, ids)

	var b strings.Builder
	c.render(&b)
	return b.String()
}

// revealSeconds is how long a newly done node takes to reveal its hyphae.
const revealSeconds = 1.5

// topoSignature names the nodes and dependency edges, so a refresh that only
// changed statuses keeps the grown hyphae.
func topoSignature(nodes map[string]*gnode) string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id)
		pre := append([]string(nil), nodes[id].prereqs...)
		sort.Strings(pre)
		for _, p := range pre {
			b.WriteByte('<')
			b.WriteString(p)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Hypha colours: mature mycelium is tan to brown; the primary filaments
// between nodes are the palest, laterals and germ tubes darker.
var (
	hyphaPrimary = rgb{222, 190, 142}
	hyphaLateral = rgb{176, 140, 100}
	hyphaGerm    = rgb{150, 114, 82}
	hyphaTip     = rgb{255, 242, 210}
)

// drawGrowth reveals the cached hyphae. A done node shows all of its own
// (dimmer the longer ago it finished), after playing them back in growth
// order once when it has just finished; a running node advances a few bright
// tips on the shared one-second beat; anything else shows none. Nothing here
// allocates or draws randomly, so a settled graph draws the same frame.
func (v *view) drawGrowth(c *canvas, ids []string, now float64) {
	if v.grown == nil {
		return
	}
	for _, id := range ids {
		ds := v.grown.owner[id]
		if len(ds) == 0 {
			continue
		}
		g, vn := v.nodes[id], v.vis[id]
		last := v.grown.maxStep[id]
		reveal, bright, running := last, 1.0, false
		switch g.status {
		case "done":
			if vn.grow < 1 {
				reveal = int(vn.grow * float64(last))
			}
			if vn.doneRank > 0 {
				bright = 1 - 0.09*float64(min(v.doneSeq-vn.doneRank, 4))
			} else {
				bright = 0.6
			}
		case "cancelled":
			bright = 0.3
		case "running":
			running = true
			reveal = last * (int(now)%5 + 1) / 8
		default:
			continue
		}
		for _, d := range ds {
			s := int(d.step)
			if s > reveal {
				break // dots are in step order
			}
			col := hyphaPrimary
			switch d.kind {
			case kindLateral:
				col = hyphaLateral
			case kindGerm:
				col = hyphaGerm
			}
			k := bright * math.Pow(0.86, float64(d.gen))
			pri := k
			switch {
			case running && s > reveal-4:
				col, pri = thermal(0.95), 2
			case running:
				col = thermal(0.62).scale(0.7)
			case reveal < last && s > reveal-5:
				col, pri = hyphaTip, 2
			case g.status == "cancelled":
				col = rgb{120, 120, 128}.scale(k)
			default:
				col = col.scale(k)
			}
			c.dot(int(d.x), int(d.y), col, pri)
		}
	}
}
