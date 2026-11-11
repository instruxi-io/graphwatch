package main

import (
	"fmt"
	"strings"
	"testing"
)

// deepChain is a 30-layer chain whose head is running, so the view's
// automatic focus sits at the top and the tail starts below the screen.
func deepChain() ([]apiNode, []apiEdge) {
	var ns []apiNode
	var es []apiEdge
	for i := 0; i < 30; i++ {
		st := "pending"
		if i == 0 {
			st = "running"
		}
		ns = append(ns, apiNode{ID: fmt.Sprintf("n%02d", i), Key: fmt.Sprintf("step%02d", i), Status: st})
		if i > 0 {
			es = append(es, apiEdge{fmt.Sprintf("n%02d", i), fmt.Sprintf("n%02d", i-1), "requires"})
		}
	}
	return ns, es
}

func settle(v *view, frames int) string {
	tick := simulated(v, 15)
	var out string
	for i := 0; i < frames; i++ {
		tick()
		out = v.frame(100, 30, 1.0/15)
	}
	return out
}

func TestScrollReachesTheBottomOfADeepGraph(t *testing.T) {
	v := newView()
	v.apply(deepChain())
	if out := settle(v, 60); strings.Contains(out, "step29") || !strings.Contains(out, "step00") {
		t.Fatal("control: the tail should start off screen and the head on it")
	}
	if !v.scrollKey("G") {
		t.Fatal("G not consumed")
	}
	out := settle(v, 60)
	if !strings.Contains(out, "step29") {
		t.Error("G should bring the last layer on screen")
	}
	if strings.Contains(out, "step00") {
		t.Error("G should scroll the first layer off")
	}
	v.scrollKey("g")
	if out := settle(v, 60); !strings.Contains(out, "step00") || strings.Contains(out, "step29") {
		t.Error("g should return to the top")
	}
	// Paging moves down by less than a screen and stops at the bottom.
	for i := 0; i < 20; i++ {
		v.scrollKey("pgdown")
		settle(v, 2)
	}
	if v.pin != 29 {
		t.Errorf("pgdown should stop at the last layer, pin=%d", v.pin)
	}
	v.scrollKey("f")
	if out := settle(v, 60); !strings.Contains(out, "step00") {
		t.Error("f should return to following the busy layer")
	}
}

func TestSelectionScrollsIntoView(t *testing.T) {
	v := newView()
	v.apply(deepChain())
	v.insp = newInspector()
	settle(v, 30)
	for i := 0; i < 30; i++ { // the first press selects the top node
		v.insp.key("down")
		settle(v, 3)
	}
	if got := v.selected(); got != "n29" {
		t.Fatalf("selection = %q, want n29", got)
	}
	if out := settle(v, 60); !strings.Contains(out, "step29") {
		t.Error("moving the selection down should scroll the view with it")
	}
}

func TestScrollKeysIgnoredInMycelium(t *testing.T) {
	v := newView()
	v.mycelium = true
	if v.scrollKey("G") {
		t.Error("the radial view fits the screen; G should pass through")
	}
}

func TestParsePageKeys(t *testing.T) {
	got := strings.Join(parseKeys([]byte("\x1b[5~\x1b[6~\x1b[H\x1b[F\x1bOH\x1b[1~\x1b[4~x")), ",")
	if want := "pgup,pgdown,home,end,home,home,end,x"; got != want {
		t.Errorf("parseKeys = %s, want %s", got, want)
	}
}
