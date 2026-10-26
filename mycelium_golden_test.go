package main

import "testing"

// Mycelium growth is seeded from node ids with no clock and no randomness, so
// the hyphae of the fixed demo plan are the same on every run. This pins what
// a person sees mid-growth and settled, 100x36, as the emulator renders it,
// with the travelling particles removed: their speeds are drawn in map
// iteration order, so they are the one part of a frame that is not stable.
func TestMyceliumGoldenPinned(t *testing.T) {
	mid, settled := demoFramesOpt(100, 36, true)
	checkGolden(t, "mycelium_mid.txt", mid)
	checkGolden(t, "mycelium_settled.txt", settled)
}
