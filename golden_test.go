package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the files under testdata/ from the current output:
//
//	go test ./... -run 'GoldenPinned' -update
//
// Do it only when a change to what is drawn is intended, and review the diff.
var update = flag.Bool("update", false, "rewrite the golden files under testdata/")

// checkGolden compares got with testdata/<name> in full. On a mismatch it
// shows the first lines that differ, since a bare "output changed" cannot be
// acted on.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	wantB, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	if want := string(wantB); want != got {
		t.Errorf("%s differs from the golden file (rerun with -update if the change is intended):\n%s", name, firstDiff(want, got))
	}
}

// firstDiff is a minimal unified-style diff: up to five differing lines, each
// shown as a -want / +got pair with its 1-based line number.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) && i < len(g) && wl == gl {
			continue
		}
		fmt.Fprintf(&b, "@@ line %d @@\n-%q\n+%q\n", i+1, wl, gl)
		if shown++; shown == 5 {
			break
		}
	}
	if b.Len() == 0 {
		return "(no line differs)"
	}
	return b.String()
}
