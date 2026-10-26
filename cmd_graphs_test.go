package main

import (
	"strings"
	"testing"
)

func graphsFake(t *testing.T) *fakeAPI {
	id1 := "11111111-aaaa-bbbb-cccc-000000000001"
	id2 := "22222222-aaaa-bbbb-cccc-000000000002"
	return newFakeAPI(t, map[string]any{
		"/graphs": []map[string]any{
			{"id": id1, "slug": "alpha", "name": "Alpha plan", "mode": "dag", "lifecycle": "terminal"},
			{"id": id2, "slug": "old", "name": "Old plan", "mode": "dag", "lifecycle": "open", "archived_at": "2026-01-01T00:00:00Z"},
		},
		"/graphs/" + id1: map[string]any{"id": id1, "slug": "alpha", "name": "Alpha plan", "mode": "dag", "lifecycle": "terminal", "epoch": 1, "node_count": 3, "edge_count": 2},
		"/graphs/" + id1 + "/summary": map[string]any{"state": "recruiting", "complete": false,
			"by_status":     map[string]any{"done": 1, "active": 2},
			"by_work_state": map[string]any{"finished": 1, "looking_for_work": 1, "not_yet_available": 1},
			"workers":       map[string]any{"active": 2}, "demand": map[string]any{"workers_wanted": 1}},
		"/graphs/" + id1 + "/stats": map[string]any{"totals": map[string]any{"nodes": 3, "nodes_done": 1}},
	})
}

func TestGraphsListTable(t *testing.T) {
	f := graphsFake(t)
	out, _, code := runCLI(t, f, "graphs")
	if code != 0 || !strings.Contains(out, "11111111 ") || strings.Contains(out, "11111111-") ||
		!strings.Contains(out, "alpha") || strings.Contains(out, "Old plan") {
		t.Fatalf("code %d, out:\n%s", code, out)
	}
	out, _, _ = runCLI(t, f, "graphs", "--archived", "--full-ids")
	if !strings.Contains(out, "Old plan") || !strings.Contains(out, "11111111-aaaa-bbbb-cccc-000000000001") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestGraphsShowSummary(t *testing.T) {
	f := graphsFake(t)
	out, _, code := runCLI(t, f, "graphs", "show", "alpha")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"== identity", "recruiting", "looking_for_work", "not_yet_available", "workers_wanted", "nodes_done"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	out, _, code = runCLI(t, f, "graphs", "show", "alpha", "--json")
	if code != 0 || !strings.Contains(out, `"graph"`) || !strings.Contains(out, `"summary"`) || !strings.Contains(out, `"stats"`) {
		t.Fatalf("json code %d:\n%s", code, out)
	}
}

func TestGraphsSlugResolution(t *testing.T) {
	f := graphsFake(t)
	_, errOut, code := runCLI(t, f, "graphs", "show", "alp")
	if code != exitUsage || !strings.Contains(errOut, "alpha") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	_, errOut, code = runCLI(t, f, "graphs", "show", "zzz")
	if code != exitUsage || !strings.Contains(errOut, "no graph") {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestGraphsReadOnly(t *testing.T) {
	f := graphsFake(t)
	runCLI(t, f, "graphs")
	runCLI(t, f, "graphs", "show", "11111111-aaaa-bbbb-cccc-000000000001")
	if len(f.requests()) == 0 {
		t.Fatal("no requests recorded")
	}
	f.onlyReads(t)
}
