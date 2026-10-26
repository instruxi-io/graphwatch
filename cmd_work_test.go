package main

import (
	"strings"
	"testing"
	"time"
)

var lobbyFixture = map[string]any{"/work": map[string]any{"total": 2, "limit": 200, "offset": 0, "items": []any{
	map[string]any{"graph_id": "g1", "name": "alpha", "state": "looking_for_work", "workers_wanted": 3, "judges_wanted": 0, "priority": 50, "opens_next_at": "2026-10-08T00:00:00Z"},
	map[string]any{"graph_id": "g2", "name": "beta", "state": "looking_for_validation", "workers_wanted": 0, "judges_wanted": 2, "priority": 10},
}}}

func TestWorkTableAndHeader(t *testing.T) {
	f := newFakeAPI(t, lobbyFixture)
	out, _, code := runCLI(t, f, "work")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	lines := strings.Split(out, "\n")
	if want := "work: 2 graphs, workers_wanted=3 judges_wanted=2; by work_state: looking_for_validation=1 looking_for_work=1"; lines[0] != want {
		t.Errorf("header %q", lines[0])
	}
	for _, w := range []string{"GRAPH", "WORK_STATE", "DEMAND", "OPENS_AT", "alpha", "work=3 validation=0", "2026-10-08T00:00:00Z", "beta"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
	f.onlyReads(t)
}

func TestWorkFilters(t *testing.T) {
	f := newFakeAPI(t, lobbyFixture)
	out, _, _ := runCLI(t, f, "work", "--state", "looking_for_validation")
	if strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("state filter:\n%s", out)
	}
	out, _, _ = runCLI(t, f, "work", "--graph", "g1")
	if strings.Contains(out, "beta") || !strings.Contains(out, "alpha") {
		t.Errorf("graph filter:\n%s", out)
	}
	runCLI(t, f, "work", "--kind", "validation", "--tier", "task")
	last := f.requests()[len(f.requests())-1]
	if !strings.Contains(last, "kind=validation") || !strings.Contains(last, "type=task") {
		t.Errorf("server filters not sent: %s", last)
	}
}

func TestRecruitingOutput(t *testing.T) {
	f := newFakeAPI(t, map[string]any{"/graphs/g1/recruiting": map[string]any{"graph_id": "g1",
		"recruiting": map[string]any{"open": true, "audience": "groups", "groups": []string{"platform"}, "max_parallel": 4, "priority": 70}}})
	out, _, code := runCLI(t, f, "recruiting", "g1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, w := range []string{"open=true", "audience=groups", "platform", "max_parallel", "4", "70"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
	f.onlyReads(t)
}

func TestWorkWatchMinimumInterval(t *testing.T) {
	for _, n := range []int{-3, 0, 1, 4} {
		if got := watchInterval(n); got != 5*time.Second {
			t.Errorf("watchInterval(%d)=%v", n, got)
		}
	}
	if watchInterval(30) != 30*time.Second {
		t.Error("30 should pass through")
	}
}
