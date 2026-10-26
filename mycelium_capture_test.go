package main

import (
	"os"
	"strings"
	"testing"
)

// vtText is what a person sees: the emulator's runes, one line per row.
func vtText(t *vt) string {
	var b strings.Builder
	for y := 0; y < t.h; y++ {
		for x := 0; x < t.w; x++ {
			b.WriteRune(t.cells[y*t.w+x].r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// setStatus sets the named nodes' status in place.
func setStatus(ns []apiNode, st string, keys ...string) {
	for i := range ns {
		for _, k := range keys {
			if ns[i].Key == k {
				ns[i].Status = st
			}
		}
	}
}

// demoFrames plays the demo plan through the diff renderer into the vt
// emulator and returns the two frames a person would see: mid-growth (a node
// just finished and is revealing its hyphae, two running) and settled.
func demoFrames(w, h int) (mid, settled string) {
	return demoFramesOpt(w, h, false)
}

// demoFramesOpt is demoFrames; noParticles clears the travelling particles
// before every frame. Particles take their speeds from a shared generator
// whose draw order follows map iteration, so they differ between runs; the
// hyphae and everything else drawn do not.
func demoFramesOpt(w, h int, noParticles bool) (mid, settled string) {
	const fps = 15
	v := newView()
	v.mycelium = true
	v.title = "demo"
	tick := simulated(v, fps)
	ns, es := demoPlan()
	setStatus(ns, "done", "spec", "schema", "infra")
	setStatus(ns, "running", "parse", "migrate")
	v.apply(ns, es)
	term := newVT(w, h)
	var scr screen
	run := func(frames int) {
		for i := 0; i < frames; i++ {
			tick()
			if noParticles {
				v.parts = nil
			}
			term.write(scr.diff(v.frame(w, h, 1.0/fps)))
		}
	}
	run(4 * fps)
	setStatus(ns, "done", "parse", "lint", "seed", "cluster")
	setStatus(ns, "running", "migrate", "cache", "compile")
	v.apply(ns, es)
	run(fps * 7 / 10)
	mid = vtText(term)
	for i := range ns {
		ns[i].Status = "done"
	}
	v.apply(ns, es)
	run(12 * fps)
	settled = vtText(term)
	return mid, settled
}

// TestMyceliumCaptureFrames writes the two demo frames to $GRAPHWATCH_CAPTURE
// when it is set, so a person (or the PR) can look at the actual output.
func TestMyceliumCaptureFrames(t *testing.T) {
	mid, settled := demoFrames(100, 36)
	v := newView()
	v.mycelium = true
	v.apply(demoPlan())
	v.frame(100, 36, 0.1)
	t.Logf("dots %d / budget %d, branches %d, edges %d", v.grown.dots, v.grown.budget, v.grown.branches, v.grown.edges)
	if mid == settled {
		t.Fatal("mid-growth and settled frames are identical")
	}
	if p := os.Getenv("GRAPHWATCH_CAPTURE"); p != "" {
		out := "mid-growth\n" + mid + "\nsettled\n" + settled
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
