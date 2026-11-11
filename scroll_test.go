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

func TestParseModifiedArrows(t *testing.T) {
	got := strings.Join(parseKeys([]byte("\x1b[1;5A\x1b[1;5D\x1b[1;2B\x1b[1;2C\x1b[1;3Aq")), ",")
	// Alt+up is swallowed whole: none of its bytes may leak as keys.
	if want := "ctrl-up,ctrl-left,shift-down,shift-right,q"; got != want {
		t.Errorf("parseKeys = %s, want %s", got, want)
	}
}

// frameText renders a frame back to plain rows, for asserting what is where.
func frameText(f string) []string {
	cells, w, h := parseFrame(f)
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		r := make([]rune, w)
		for x := 0; x < w; x++ {
			r[x] = cells[y*w+x].r
		}
		rows[y] = strings.TrimRight(string(r), " ")
	}
	return rows
}

func TestScrolledGraphStaysOutOfTheChrome(t *testing.T) {
	v := newView()
	v.apply(deepChain())
	v.scrollKey("G")
	rows := frameText(settle(v, 60))
	for _, y := range []int{2, 26, 27, 28, 29} {
		if strings.ContainsAny(rows[y], "●○◎◐") || strings.Contains(rows[y], "step") {
			t.Errorf("row %d draws graph under the chrome: %q", y, rows[y])
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(rows[0]), "◉ graphwatch") {
		t.Errorf("header overdrawn: %q", rows[0])
	}
}

func TestFitShowsEveryLayer(t *testing.T) {
	v := newView()
	v.apply(deepChain())
	out := settle(v, 60)
	count := func(s string) int { return strings.Count(s, "○") + strings.Count(s, "◐") + strings.Count(s, "◎") }
	if n := count(out); n >= 30 {
		t.Fatalf("control: a 30-layer chain should not fit 30 rows unaided, drew %d nodes", n)
	}
	v.scrollKey("0")
	settle(v, 60)
	for id, vn := range v.vis {
		if vn.y < 4 || vn.y > 30-6 {
			t.Errorf("0 should fit every layer in the band; %s at row %.1f", id, vn.y)
		}
	}
	v.scrollKey("f")
	if v.cam != (camera{}) || v.pin != -1 {
		t.Errorf("f should reset the camera, got %+v pin=%d", v.cam, v.pin)
	}
}

func TestZoomAndPan(t *testing.T) {
	v := newView()
	v.apply(deepChain())
	settle(v, 10)
	gap := v.lastGap
	v.scrollKey("shift-up")
	settle(v, 2)
	if v.lastGap != gap+1 {
		t.Errorf("shift-up: gap %d -> %d, want +1", gap, v.lastGap)
	}
	v.scrollKey("shift-down")
	v.scrollKey("shift-down") // two presses inside one frame both count
	settle(v, 60)
	if v.lastGap != gap-1 {
		t.Errorf("shift-down x2: gap = %d, want %d", v.lastGap, gap-1)
	}
	y := v.vis["n05"].y
	v.scrollKey("ctrl-down")
	settle(v, 60)
	if d := y - v.vis["n05"].y; d < 2.5 || d > 3.5 {
		t.Errorf("ctrl-down should move the graph up 3 rows, moved %.1f", d)
	}
	x := v.vis["n05"].x
	v.scrollKey("ctrl-right")
	settle(v, 60)
	if d := x - v.vis["n05"].x; d < 7.5 || d > 8.5 {
		t.Errorf("ctrl-right should move the graph left 8 cols, moved %.1f", d)
	}
}

func TestWatchExitKeys(t *testing.T) {
	for _, c := range []struct {
		k             string
		picker        bool
		leave, toList bool
	}{
		{"esc", false, true, true}, {"b", false, true, true}, {"backspace", false, true, true},
		{"q", false, true, false}, {"q", true, true, true}, {"ctrl-c", true, true, false},
		{"j", false, false, false}, {"G", true, false, false},
	} {
		if l, b := watchExit(c.k, c.picker); l != c.leave || b != c.toList {
			t.Errorf("watchExit(%q, picker=%v) = %v,%v want %v,%v", c.k, c.picker, l, b, c.leave, c.toList)
		}
	}
}
