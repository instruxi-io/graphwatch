package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// chainPlan: root r, then ten done nodes "xeni-0".."xeni-9" in a chain under
// it (r done too), then an active node "next" needing the last of them, and
// a loose done pair "solo-a" <- "solo-b" off to the side.
func chainPlan() ([]apiNode, []apiEdge) {
	at := time.Unix(1_700_000_000, 0)
	ns := []apiNode{{ID: "r", Key: "root", Status: "done", CreatedAt: at}}
	es := []apiEdge{}
	prev := "r"
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("x%d", i)
		ns = append(ns, apiNode{ID: id, Key: fmt.Sprintf("xeni-%d", i), Status: "done", CreatedAt: at})
		es = append(es, apiEdge{FromNodeID: id, ToNodeID: prev, Type: "requires"})
		prev = id
	}
	ns = append(ns, apiNode{ID: "next", Key: "next", Status: "active", CreatedAt: at},
		apiNode{ID: "sa", Key: "solo-a", Status: "done", CreatedAt: at},
		apiNode{ID: "sb", Key: "solo-b", Status: "done", CreatedAt: at})
	es = append(es, apiEdge{FromNodeID: "next", ToNodeID: prev, Type: "requires"},
		apiEdge{FromNodeID: "sb", ToNodeID: "sa", Type: "requires"})
	return ns, es
}

func condensedView(t *testing.T, at int) *view {
	t.Helper()
	v := newView()
	v.insp = newInspector()
	v.condenseAt = at
	ns, es := chainPlan()
	v.apply(ns, es)
	clk := time.Unix(0, 0)
	v.clock, v.started = func() time.Time { return clk }, clk
	v.frame(120, 40, 0.05)
	return v
}

func TestCondenseGroupsFinishedComponent(t *testing.T) {
	v := condensedView(t, 8)
	g := v.shown["group:r"]
	if g == nil || g.members != 11 {
		t.Fatalf("want one group of 11 (root and the chain), shown = %v", keysOf(v.shown))
	}
	if g.key != "×11 xeni" {
		t.Errorf("label = %q, want the common prefix and count", g.key)
	}
	if got := v.shown["next"].prereqs; len(got) != 1 || got[0] != "group:r" {
		t.Errorf("next's prerequisite = %v, want the group", got)
	}
	if len(g.prereqs) != 0 || len(g.children) != 1 || g.children[0] != "next" {
		t.Errorf("group edges: prereqs %v children %v, want none and [next]", g.prereqs, g.children)
	}
	if v.shown["sa"] == nil || v.shown["sb"] == nil {
		t.Error("a pair below the threshold must stay drawn node by node")
	}
	if !v.ready(v.shown["next"]) {
		t.Error("next depends only on finished work: ready")
	}
	if len(v.shown) != 4 {
		t.Errorf("shown = %v, want group, next, sa, sb", keysOf(v.shown))
	}
}

func TestCondenseOffAndBelowThreshold(t *testing.T) {
	if v := condensedView(t, 0); len(v.shown) != 14 {
		t.Errorf("--condense 0: %d shown, want all 14", len(v.shown))
	}
	if v := condensedView(t, 12); len(v.shown) != 14 {
		t.Errorf("threshold above the group: %d shown, want all 14", len(v.shown))
	}
}

func TestGroupKeysOpenFoldToggle(t *testing.T) {
	v := condensedView(t, 8)
	v.selectID("group:r")
	if !v.groupKey("enter") {
		t.Fatal("enter on a group must be the group's key")
	}
	v.frame(120, 40, 0.05)
	if v.shown["group:r"] != nil || len(v.shown) != 14 {
		t.Fatalf("after enter: %v, want every node", keysOf(v.shown))
	}
	if sel := v.selected(); sel != "r" {
		t.Errorf("selection after opening = %q, want the group's first member", sel)
	}
	// Enter on a plain node is the inspector's.
	if v.groupKey("enter") {
		t.Error("enter on a plain node must fall through to the inspector")
	}
	v.selectID("x4")
	v.groupKey("x")
	v.frame(120, 40, 0.05)
	if v.shown["group:r"] == nil || v.selected() != "group:r" {
		t.Fatalf("x on a member folds the group and selects it; shown %v sel %q", keysOf(v.shown), v.selected())
	}
	v.groupKey("c")
	v.frame(120, 40, 0.05)
	if len(v.shown) != 14 {
		t.Errorf("c: condensing off, want 14 shown, got %d", len(v.shown))
	}
	v.groupKey("c")
	v.frame(120, 40, 0.05)
	if v.shown["group:r"] == nil {
		t.Error("c again: condensing back on")
	}
}

func TestSelectionInsideGroupOpensIt(t *testing.T) {
	v := condensedView(t, 8)
	v.selectID("x7") // the needs-you panel goes straight to a node
	v.frame(120, 40, 0.05)
	if v.shown["x7"] == nil || v.shown["group:r"] != nil {
		t.Errorf("a selection inside a closed group must open it; shown %v", keysOf(v.shown))
	}
}

func TestCondensedHeaderCountsRealNodes(t *testing.T) {
	v := condensedView(t, 8)
	out := v.frame(120, 40, 0.05)
	if !strings.Contains(stripANSI(out), "13 done") {
		t.Errorf("header must count the 13 real done nodes, not the group: %q", firstLines(stripANSI(out), 2))
	}
}

func TestNodeFinishingJoinsGroup(t *testing.T) {
	v := condensedView(t, 8)
	ns, es := chainPlan()
	for i := range ns {
		if ns[i].ID == "next" {
			ns[i].Status = "done"
		}
	}
	v.apply(ns, es)
	v.frame(120, 40, 0.05)
	if g := v.shown["group:next"]; g == nil && (v.shown["group:r"] == nil || v.shown["group:r"].members != 12) {
		t.Errorf("next finished: the group must take it in; shown %v", keysOf(v.shown))
	}
}

// Condensing must not bring back the endless glide: a big partly finished
// graph with groups settles like any other.
func TestCondensedBigGraphSettles(t *testing.T) {
	for _, seed := range []uint64{10, 14} {
		ns, es := bigDAG(seed)
		for i := range ns {
			if i < 180 {
				ns[i].Status = "done"
			}
		}
		v := newView()
		v.condenseAt = 8
		v.apply(ns, es)
		clk := time.Unix(0, 0)
		v.clock, v.started = func() time.Time { return clk }, clk
		var scr screen
		sent := 0
		for f := 1; f <= 600; f++ {
			clk = clk.Add(time.Second / 15)
			out := scr.diff(v.frame(200, 60, 1.0/15))
			if f > 450 {
				sent += len(out)
			}
		}
		groups := 0
		for id := range v.shown {
			if isGroupID(id) {
				groups++
			}
		}
		if groups == 0 {
			t.Errorf("seed %d: expected at least one group", seed)
		}
		if sent > 2000 {
			t.Errorf("seed %d: %d bytes in the last 10 s, want a settled view", seed, sent)
		}
	}
}

func keysOf(m map[string]*gnode) []string {
	var out []string
	for id := range m {
		out = append(out, id)
	}
	return out
}

func firstLines(s string, n int) string {
	ls := strings.SplitN(s, "\n", n+1)
	return strings.Join(ls[:min(n, len(ls))], "\n")
}

// stripANSI drops escape sequences, leaving the drawn text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i++; i < len(s) && (s[i] < '@' || s[i] > '~' || s[i] == '['); i++ {
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
