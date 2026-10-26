package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fp(f float64) *float64 { return &f }

func inspNode() apiNodeFull {
	return apiNodeFull{Key: "build", Title: "Build it", Status: "verifying", WorkState: "verifying",
		Data: json.RawMessage(`{"tier":"standard","tier_source":"user","acceptance":["tests pass","pr merged"]}`)}
}

func TestInspectorLinesRunAndVerdict(t *testing.T) {
	st := inspState{Node: inspNode(), Held: true, Now: time.Unix(1000, 0),
		Run: &apiRun{Attempt: 2, Status: "succeeded", Runner: "w1", Evidence: []apiEvidence{{Kind: "command"}},
			Verification: &apiVerification{State: "verified", Confidence: fp(0.9),
				Criteria: []apiCriterion{{Text: "tests pass", Noul: fp(0.95)}, {Text: "pr merged", Noul: fp(0.1)}}},
			Validation: &apiValidation{State: "pending", Tally: &apiTally{Accept: 1, Undecided: 2}}}}
	got := strings.Join(inspectorLines(st, 80), "\n")
	for _, want := range []string{"build  Build it", "work_state verifying", "HELD", "tier standard (source user)",
		"run attempt 2 · succeeded · runner w1", "verdict verified · confidence 0.90",
		"1. MET  tests pass", "2. NOT MET  pr merged", "votes pending (accept 1 · reject 0 · undecided 2)",
		"evidence 1 item(s)", "acceptance"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, l := range inspectorLines(st, 20) {
		if len([]rune(l)) > 20 {
			t.Errorf("line wider than 20: %q", l)
		}
	}
}

func TestInspectorLinesRoute(t *testing.T) {
	st := inspState{Node: inspNode(), Route: &apiRoute{Tier: "standard", Model: "sonnet", Reason: "planned tier"}}
	got := strings.Join(inspectorLines(st, 80), "\n")
	if !strings.Contains(got, "route tier standard · model sonnet") || !strings.Contains(got, "reason planned tier") {
		t.Errorf("route lines:\n%s", got)
	}
	st.Route = &apiRoute{RouteUndecided: true}
	if got := strings.Join(inspectorLines(st, 80), "\n"); !strings.Contains(got, "route undecided") {
		t.Errorf("undecided:\n%s", got)
	}
	st.Route, st.RouteErr = nil, "boom"
	if got := strings.Join(inspectorLines(st, 80), "\n"); !strings.Contains(got, "route: unavailable (boom)") {
		t.Errorf("err:\n%s", got)
	}
}

func TestInspectorSelectionMovesNearest(t *testing.T) {
	pos := map[string][2]float64{"a": {10, 5}, "b": {10, 15}, "c": {30, 5}, "d": {12, 25}}
	for _, tc := range []struct{ cur, dir, want string }{
		{"a", "down", "b"}, {"b", "down", "d"}, {"a", "right", "c"}, {"c", "left", "a"},
		{"a", "up", "a"}, {"d", "up", "b"}, {"", "down", "a"},
	} {
		if got := selectNearest(pos, tc.cur, tc.dir); got != tc.want {
			t.Errorf("%q %s: got %q want %q", tc.cur, tc.dir, got, tc.want)
		}
	}
	in := newInspector()
	in.pos = pos
	if !in.key("j") || in.sel != "a" {
		t.Errorf("first move selects top-left, got %q", in.sel)
	}
	in.key("j")
	if in.sel != "b" {
		t.Errorf("j: %q", in.sel)
	}
	if in.key("esc") {
		t.Error("esc on a closed panel must fall through")
	}
	in.key("enter")
	if !in.open || !in.key("esc") || in.open {
		t.Error("enter opens, esc closes")
	}
}

func TestInspectorHeldMarker(t *testing.T) {
	if !isHeld("verifying", false) || !isHeld("running", true) || isHeld("done", false) {
		t.Error("isHeld rule")
	}
	v := newView()
	tick := simulated(v, 15)
	ns, es := testGraph()
	ns[1].Status = "verifying"
	v.apply(ns, es)
	v.insp = newInspector()
	for i := 0; i < 20; i++ {
		tick()
		v.frame(100, 36, 1.0/15)
	}
	if out := v.frame(100, 36, 1.0/15); !strings.Contains(out, "⟨") || !strings.Contains(out, "⟩") {
		t.Error("a verifying node should be ringed")
	}
}

func TestInspectorNoGoldenChange(t *testing.T) {
	run := func(insp *inspector) string {
		v := newView()
		tick := simulated(v, 15)
		v.apply(testGraph())
		v.insp = insp
		var b strings.Builder
		for i := 0; i < 40; i++ {
			tick()
			b.WriteString(v.frame(100, 36, 1.0/15))
		}
		return b.String()
	}
	if run(nil) != run(newInspector()) {
		t.Error("an idle inspector changed the frame")
	}
}
