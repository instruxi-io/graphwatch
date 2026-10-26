package main

import (
	"strings"
	"testing"
)

func neRoutes() map[string]any {
	nodes := []map[string]any{
		{"id": "n1", "key": "alpha", "type": "task", "title": "First", "status": "done", "work_state": "done", "data": map[string]any{"tier": "standard"}},
		{"id": "n2", "key": "beta", "type": "gate", "title": "Second", "status": "active", "work_state": "blocked"},
	}
	return map[string]any{
		"/graphs/g1/nodes": nodes,
		"/graphs/g1/nodes/n2": map[string]any{"id": "n2", "key": "beta", "type": "gate", "title": "Second", "description": "Do the thing",
			"status": "active", "work_state": "blocked", "reclaimable": true, "opens_at": "2026-01-02T03:04:05Z", "assignee": "ann",
			"data": map[string]any{"acceptance": []string{"first line", "second line"}}},
		"/graphs/g1/nodes/n2/runs": []map[string]any{
			{"id": "r1", "attempt": 1, "status": "failed", "created_at": "2026-01-01T00:00:00Z", "data": map[string]any{"runner": "worker-a"}},
		},
		"/graphs/g1/edges": []map[string]any{{"from_node_id": "n2", "to_node_id": "n1", "type": "requires"}},
	}
}

func TestNodesListFilters(t *testing.T) {
	f := newFakeAPI(t, neRoutes())
	out, _, code := runCLI(t, f, "nodes", "g1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"KEY", "WORK_STATE", "TIER", "alpha", "standard", "blocked", "beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	out, _, _ = runCLI(t, f, "nodes", "g1", "--status", "active")
	if strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("--status filter:\n%s", out)
	}
	out, _, _ = runCLI(t, f, "nodes", "g1", "--type", "task")
	if strings.Contains(out, "beta") || !strings.Contains(out, "alpha") {
		t.Errorf("--type filter:\n%s", out)
	}
}

func TestNodesShowRuns(t *testing.T) {
	f := newFakeAPI(t, neRoutes())
	out, _, code := runCLI(t, f, "nodes", "show", "g1", "beta")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"Do the thing", "blocked", "reclaimable: true", "opens_at:", "ann", "  first line", "  second line", "ATTEMPT", "worker-a", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	_, errs, code := runCLI(t, f, "nodes", "show", "g1", "nope")
	if code == 0 || !strings.Contains(errs, "no node") {
		t.Errorf("unknown key: code %d %q", code, errs)
	}
}

func TestEdgesDependsOnLabel(t *testing.T) {
	f := newFakeAPI(t, neRoutes())
	out, _, code := runCLI(t, f, "edges", "g1")
	if code != 0 || !strings.Contains(out, "beta") || !strings.Contains(out, "depends on") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if !strings.Contains(out, "beta      depends on  alpha") {
		t.Errorf("direction:\n%s", out)
	}
}

func TestEdgesAsFlowFlips(t *testing.T) {
	f := newFakeAPI(t, neRoutes())
	out, _, code := runCLI(t, f, "edges", "g1", "--as-flow")
	if code != 0 || strings.Contains(out, "depends on") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if !strings.Contains(out, "alpha         ->  beta") {
		t.Errorf("flow direction:\n%s", out)
	}
}

func TestNodesReadOnly(t *testing.T) {
	f := newFakeAPI(t, neRoutes())
	runCLI(t, f, "nodes", "g1")
	runCLI(t, f, "nodes", "show", "g1", "beta")
	runCLI(t, f, "edges", "g1")
	if len(f.requests()) == 0 {
		t.Fatal("no requests")
	}
	f.onlyReads(t)
}
