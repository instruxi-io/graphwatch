package main

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"sync"
)

// Loading a graph. The node list costs the server about the same per request
// whatever the page holds (measured 2026-11-12 on a 256-node plan: 2.4-3.2 s
// for 200 rows and for 56; the 444 edges took 0.3 s a page), and graphwatch
// used to read its pages one after another, nodes then edges. The edges are now
// read beside the nodes, a refresh sends every page it expects at once, and
// the first load feeds the view each page as it lands, behind a progress bar,
// instead of a blank screen until the last page.

const (
	pageSize    = 200
	pageWorkers = 4 // pages in flight per list, so a huge graph does not flood the API
)

// pageResult is one page as the list endpoints return it.
type pageResult[T any] struct {
	rows  []T
	total int // -1 when the server sent no total
	err   error
}

// pagesParallel reads every page of path. expect is how many rows the caller
// saw last time (0: unknown); it decides how many pages go out at once before
// the first total is known. Without one, a single page goes out: a guessed
// extra page is not free (the node list costs the server ~3 s even empty) and
// does not overlap anyway (two node pages sent together both took 5.4-6.8 s:
// the server serialises them). onPage, when set, hears every page as it
// lands with the running count and the total (-1 until known); calls never
// overlap.
func pagesParallel[T any](ctx context.Context, c *client, path string, expect int, onPage func(rows []T, got, total int)) ([]T, error) {
	byOff := map[int][]T{}
	total, next, got := -1, 0, 0
	short := false // a page came back less than full: the end, total or not
	var mu sync.Mutex
	for first := true; ; first = false {
		upto := next + pageSize // no total, no short page: one more
		switch {
		case total >= 0:
			upto = total
		case first && expect > 0:
			upto = expect
		}
		var wave []int
		for off := next; off < upto; off += pageSize {
			wave = append(wave, off)
		}
		if len(wave) == 0 || (total < 0 && short) {
			break
		}
		next = wave[len(wave)-1] + pageSize
		res := make([]pageResult[T], len(wave))
		sem := make(chan struct{}, pageWorkers)
		var wg sync.WaitGroup
		for i, off := range wave {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				var r struct {
					Data []T
					Meta struct{ Total *int }
				}
				err := c.do(ctx, http.MethodGet, path+"?limit="+strconv.Itoa(pageSize)+"&offset="+strconv.Itoa(off), nil, &r)
				pr := pageResult[T]{rows: r.Data, total: -1, err: err}
				if r.Meta.Total != nil {
					pr.total = *r.Meta.Total
				}
				res[i] = pr
				if err == nil && onPage != nil {
					mu.Lock()
					got += len(r.Data)
					t := total
					if pr.total > t {
						t = pr.total
					}
					onPage(r.Data, got, t)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		for i, r := range res {
			if r.err != nil {
				return nil, r.err
			}
			byOff[wave[i]] = r.rows
			total = max(total, r.total)
			if len(r.rows) < pageSize {
				short = true
			}
		}
		if total >= 0 && next >= total {
			break
		}
		if total < 0 && short {
			break
		}
	}
	offs := make([]int, 0, len(byOff))
	for o := range byOff {
		offs = append(offs, o)
	}
	sort.Ints(offs)
	// The list ends at its first short page. A page sent beyond it on a guess
	// is dropped even when not empty: a server that ignores offset answers
	// every offset with page one, and counting it would list each row twice.
	var all []T
	for _, o := range offs {
		all = append(all, byOff[o]...)
		if len(byOff[o]) < pageSize {
			break
		}
	}
	return all, nil
}

// loadState is the first load's progress, drawn until the whole graph is in.
type loadState struct {
	active         bool
	nodes, nodesOf int // nodesOf -1 until the server says
	edges, edgesOf int
	failed         string
}

// loadGraph reads the nodes and the edges side by side. While the view has
// not had a whole graph yet it shows progress and is fed each page of nodes
// once the edges are in, so the graph grows on screen as it arrives; after
// that a refresh replaces the graph only when both reads are complete, so
// nodes never blink out mid-read. It returns what it read for next time's
// expectations.
func loadGraph(ctx context.Context, c *client, v *view, graphID string, expect [2]int) (nodes, edges int, err error) {
	v.mu.Lock()
	first := !v.loaded
	v.mu.Unlock()
	var mu sync.Mutex
	var partN []apiNode
	var partE []apiEdge
	edgesIn := false
	feed := func() { // under mu
		if edgesIn && len(partN) > 0 {
			v.apply(dedupNodes(partN), partE)
		}
	}
	var onN func([]apiNode, int, int)
	var onE func([]apiEdge, int, int)
	if first {
		v.progress(func(l *loadState) { l.active, l.nodesOf, l.edgesOf = true, -1, -1 })
		onN = func(rows []apiNode, got, total int) {
			mu.Lock()
			defer mu.Unlock()
			partN = append(partN, rows...)
			v.progress(func(l *loadState) { l.nodes, l.nodesOf = got, total })
			feed()
		}
		onE = func(_ []apiEdge, got, total int) {
			v.progress(func(l *loadState) { l.edges, l.edgesOf = got, total })
		}
	}
	var ns []apiNode
	var es []apiEdge
	var errN, errE error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ns, errN = pagesParallel(ctx, c, "/graphs/"+graphID+"/nodes", expect[0], onN)
	}()
	go func() {
		defer wg.Done()
		es, errE = pagesParallel(ctx, c, "/graphs/"+graphID+"/edges", expect[1], onE)
		if errE == nil && first {
			mu.Lock()
			partE, edgesIn = es, true
			feed()
			mu.Unlock()
		}
	}()
	wg.Wait()
	if err = errN; err == nil {
		err = errE
	}
	if err != nil {
		if first {
			v.progress(func(l *loadState) { l.failed = err.Error() })
		}
		return expect[0], expect[1], err
	}
	ns = dedupNodes(ns)
	v.apply(ns, es)
	v.mu.Lock()
	v.loaded, v.load = true, loadState{}
	v.mu.Unlock()
	return len(ns), len(es), nil
}

// dedupNodes keeps the first of each id: offset pages read while the graph
// changes can repeat a row across a page boundary.
func dedupNodes(ns []apiNode) []apiNode {
	seen := make(map[string]bool, len(ns))
	out := ns[:0:0]
	for _, n := range ns {
		if !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}

// progress edits the load state under the view's lock.
func (v *view) progress(f func(*loadState)) {
	v.mu.Lock()
	f(&v.load)
	v.mu.Unlock()
}

// drawLoad is the first load's progress: a bar over the nodes read so far
// and a line saying what is in, centred under the header. Its shimmer moves
// once a frame only while a read is outstanding; it is gone once the graph is.
func (v *view) drawLoad(c *canvas, w, h int, now float64) {
	l := v.load
	if !l.active {
		return
	}
	y := max(5, h/2-1)
	if l.failed != "" {
		c.putCentered(w/2, y, "could not read the graph: "+l.failed, w-4, rgb{235, 64, 52}, true)
		c.putCentered(w/2, y+1, "retrying on the next event or tick", w-4, rgb{120, 126, 146}, false)
		return
	}
	of := func(got, total int) string {
		if total < 0 {
			return strconv.Itoa(got)
		}
		return strconv.Itoa(got) + "/" + strconv.Itoa(total)
	}
	bw := min(48, w-10)
	frac := 0.0
	if l.nodesOf > 0 {
		frac = float64(l.nodes) / float64(l.nodesOf)
	}
	fill := int(frac*float64(bw) + 0.5)
	shine := int(now*24) % (bw + 8) // a highlight sweeping the unfilled part
	x0 := w/2 - bw/2
	c.put(x0-1, y, "▕", rgb{70, 72, 90}, false)
	for i := 0; i < bw; i++ {
		switch {
		case i < fill:
			c.put(x0+i, y, "█", rgb{64, 196, 160}.mix(thermal(0.8), float64(i)/float64(bw)*0.5), false)
		case i >= shine-3 && i <= shine:
			c.put(x0+i, y, "░", rgb{150, 156, 178}, false)
		default:
			c.put(x0+i, y, "░", rgb{58, 60, 76}, false)
		}
	}
	c.put(x0+bw, y, "▏", rgb{70, 72, 90}, false)
	c.putCentered(w/2, y+2, "reading the plan · "+of(l.nodes, l.nodesOf)+" nodes · "+of(l.edges, l.edgesOf)+" edges", w-4, rgb{170, 176, 196}, false)
}
