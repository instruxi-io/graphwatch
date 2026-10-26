package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// vt is the smallest terminal that understands what screen.diff emits:
// cursor positioning, SGR, clear, and runes that advance the cursor.
type vt struct {
	w, h   int
	cells  []cell
	cx, cy int
	pen    string
}

func newVT(w, h int) *vt {
	t := &vt{w: w, h: h, cells: make([]cell, w*h)}
	t.clear()
	return t
}

func (t *vt) clear() {
	for i := range t.cells {
		t.cells[i] = cell{r: ' '}
	}
}

func (t *vt) write(s string) {
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] == ';' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			p := s[i+2 : j]
			switch s[j] {
			case 'H':
				yx := strings.SplitN(p, ";", 2)
				y, _ := strconv.Atoi(yx[0])
				x, _ := strconv.Atoi(yx[1])
				t.cy, t.cx = y-1, x-1
			case 'J':
				t.clear()
			case 'm':
				t.pen = p
			}
			i = j + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if t.cx < t.w && t.cy < t.h {
			c := cell{r: r, st: t.pen}
			if r == ' ' {
				c.st = ""
			}
			t.cells[t.cy*t.w+t.cx] = c
		}
		t.cx++
	}
}

// simulated gives v a clock that advances one frame per call to tick.
func simulated(v *view, fps int) (tick func()) {
	at := v.started
	v.clock = func() time.Time { return at }
	return func() { at = at.Add(time.Second / time.Duration(fps)) }
}

func animate(t *testing.T, frames, w, h int, each func(i int, full, out string)) {
	t.Helper()
	v := newView()
	tick := simulated(v, 15)
	v.apply(testGraph())
	var scr screen
	for i := 0; i < frames; i++ {
		tick()
		full := v.frame(w, h, 1.0/15)
		each(i, full, scr.diff(full))
	}
}

// Replaying only the diffs must leave the terminal showing exactly the frame.
func TestDiffsReproduceEveryFrame(t *testing.T) {
	const w, h = 100, 36
	term := newVT(w, h)
	animate(t, 120, w, h, func(i int, full, out string) {
		term.write(out)
		want, _, _ := parseFrame(full)
		for k := range want {
			if term.cells[k] != want[k] {
				t.Fatalf("frame %d: cell (%d,%d) is %q %q, want %q %q", i, k%w, k/w, term.cells[k].r, term.cells[k].st, want[k].r, want[k].st)
			}
		}
	})
}

// The point of the change: after the first frame, output is a small fraction
// of a full repaint, and the set of styles a terminal must keep is bounded.
func TestOutputIsBoundedAndSmall(t *testing.T) {
	const w, h = 100, 36
	var fullBytes, diffBytes int
	styles := map[string]bool{}
	animate(t, 300, w, h, func(i int, full, out string) {
		if i == 0 {
			return // the first frame is a full paint by design
		}
		fullBytes += len(full)
		diffBytes += len(out)
		cells, _, _ := parseFrame(full)
		for _, c := range cells {
			if c.st != "" {
				styles[c.st] = true
			}
		}
	})
	if ratio := float64(diffBytes) / float64(fullBytes); ratio > 0.25 {
		t.Errorf("diffs are %.0f%% of full repaints; want well under 25%%", ratio*100)
	}
	if len(styles) > 400 {
		t.Errorf("%d distinct styles over 300 frames; quantized output should stay small", len(styles))
	}
	t.Logf("diff/full bytes = %d/%d (%.1f%%), distinct styles = %d", diffBytes, fullBytes, 100*float64(diffBytes)/float64(fullBytes), len(styles))
}

func TestUnchangedFrameSendsNothing(t *testing.T) {
	var scr screen
	c := newCanvas(20, 4)
	c.put(1, 1, "hello", rgb{200, 100, 50}, false)
	var b strings.Builder
	c.render(&b)
	if scr.diff(b.String()) == "" {
		t.Fatal("first frame must paint")
	}
	if out := scr.diff(b.String()); out != "" {
		t.Fatalf("an identical frame sent %q", out)
	}
}

func TestResizeRepaintsFromClear(t *testing.T) {
	var scr screen
	mk := func(w, h int) string {
		c := newCanvas(w, h)
		c.put(0, 0, "x", rgb{255, 255, 255}, true)
		var b strings.Builder
		c.render(&b)
		return b.String()
	}
	scr.diff(mk(10, 3))
	if out := scr.diff(mk(12, 3)); !strings.HasPrefix(out, "\x1b[0m\x1b[2J") {
		t.Fatalf("a new size must clear first, got %q", out)
	}
	scr.diff(mk(12, 3))
	scr.reset()
	if out := scr.diff(mk(12, 3)); !strings.HasPrefix(out, "\x1b[0m\x1b[2J") {
		t.Fatalf("reset must force a clear, got %q", out)
	}
}

func TestQuantizeIsStableAndCoarse(t *testing.T) {
	seen := map[rgb]bool{}
	for v := 0; v < 256; v++ {
		q := rgb{uint8(v), uint8(v), uint8(v)}.quantize()
		if q.quantize() != q {
			t.Fatalf("quantize is not idempotent at %d", v)
		}
		seen[q] = true
	}
	if len(seen) != 16 {
		t.Fatalf("want 16 levels per channel, got %d", len(seen))
	}
}

// Event mode: with running nodes on screen and no new events, motion settles
// and the screen sends nothing, apart from the 1 Hz spinner step.
func TestQuietGraphSettlesToNoOutput(t *testing.T) {
	const w, h, fps = 100, 36, 15
	v := newView()
	tick := simulated(v, fps)
	v.apply(testGraph()) // includes a running node
	var scr screen
	var settled []int
	for i := 0; i < 40*fps; i++ { // 40 s of frames, simulated
		tick()
		out := scr.diff(v.frame(w, h, 1.0/fps))
		if i >= 30*fps {
			settled = append(settled, len(out))
		}
	}
	quiet, total := 0, 0
	for _, n := range settled {
		total += n
		if n == 0 {
			quiet++
		}
	}
	if frac := float64(quiet) / float64(len(settled)); frac < 0.85 {
		t.Errorf("after settling only %.0f%% of frames sent nothing; want at least 85%%", frac*100)
	}
	if per := total / 10; per > 2048 {
		t.Errorf("settled output is %d bytes/s; want under 2 KB/s", per)
	}
	t.Logf("settled: %d/%d frames empty, %d bytes/s", quiet, len(settled), total/10)
}

// --motion continuous keeps the old always-moving behaviour.
func TestContinuousMotionKeepsDrawing(t *testing.T) {
	v := newView()
	v.continuous = true
	tick := simulated(v, 15)
	v.apply(testGraph())
	var scr screen
	empty := 0
	for i := 0; i < 40*15; i++ {
		tick()
		if out := scr.diff(v.frame(100, 36, 1.0/15)); i >= 30*15 && out == "" {
			empty++
		}
	}
	if empty > 15 {
		t.Errorf("continuous motion produced %d empty frames in the last 10 s", empty)
	}
}

// A crowded graph — one batch, many siblings per layer, long overlapping
// labels — must settle too. Draw order once came from map iteration, so
// overlapping labels and tied edge dots flickered forever on real plans.
func TestCrowdedGraphSettles(t *testing.T) {
	var ns []apiNode
	var es []apiEdge
	ns = append(ns, apiNode{ID: "root", Key: "root", Status: "done"})
	for i := 0; i < 60; i++ {
		id := "n" + strconv.Itoa(i)
		st := "done"
		if i%9 == 0 {
			st = "running"
		}
		ns = append(ns, apiNode{ID: id, Key: "a-rather-long-node-key-" + strconv.Itoa(i), Status: st})
		es = append(es, apiEdge{id, "root", "requires"})
	}
	const w, h, fps = 100, 36, 15
	for trial := 0; trial < 3; trial++ { // map order differs per run; check more than once
		v := newView()
		tick := simulated(v, fps)
		v.apply(ns, es)
		var scr screen
		quiet, n := 0, 0
		for i := 0; i < 40*fps; i++ {
			tick()
			out := scr.diff(v.frame(w, h, 1.0/fps))
			if i >= 30*fps {
				n++
				if out == "" {
					quiet++
				}
			}
		}
		if frac := float64(quiet) / float64(n); frac < 0.85 {
			t.Fatalf("trial %d: only %.0f%% of settled frames were empty on a crowded graph", trial, frac*100)
		}
	}
}

func TestNeedsReviewRendersGlyphAndLegend(t *testing.T) {
	v := newView()
	ns, es := testGraph()
	ns[2].Status = "needs_review"
	v.apply(ns, es)
	out := v.frame(120, 36, 0.05)
	if !strings.Contains(out, "◈") || !strings.Contains(out, "needs_review") {
		t.Errorf("frame lacks the needs_review glyph or legend entry")
	}
}
