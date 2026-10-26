package main

import (
	"strings"
	"testing"
	"time"
)

func reviewRoutes() map[string]any {
	ts := time.Now().Add(-3 * time.Hour).Format(time.RFC3339)
	return map[string]any{
		"/graphs/g1/review": []map[string]any{
			{"id": "aaaaaaaa-1111", "kind": "verdict_needed", "node_id": "n1", "run_id": "r1", "state": "open", "reason": "judge escalated", "created_at": ts},
			{"id": "bbbbbbbb-2222", "kind": "approval", "node_id": "n2", "run_id": "r2", "state": "resolved", "reason": "approved", "created_at": ts},
		},
		"/graphs/g1/nodes": []map[string]any{{"id": "n1", "key": "alpha"}, {"id": "n2", "key": "beta"}, {"id": "n3", "key": "gamma"}},
		"/graphs/g1/edges": []map[string]any{
			{"from_node_id": "n3", "to_node_id": "n1", "type": "requires"},
			{"from_node_id": "n1", "to_node_id": "n2", "type": "requires"},
		},
	}
}

func TestReviewListOpenOnly(t *testing.T) {
	f := newFakeAPI(t, reviewRoutes())
	out, _, code := runCLI(t, f, "review", "g1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"ITEM", "KIND", "REASON", "aaaaaaaa", "verdict_needed", "open", "3h"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "bbbbbbbb") {
		t.Errorf("resolved item listed by default:\n%s", out)
	}
	f.onlyReads(t)
}

func TestReviewAll(t *testing.T) {
	f := newFakeAPI(t, reviewRoutes())
	out, _, code := runCLI(t, f, "review", "g1", "--all")
	if code != 0 || !strings.Contains(out, "aaaaaaaa") || !strings.Contains(out, "bbbbbbbb") {
		t.Fatalf("code %d:\n%s", code, out)
	}
}

func TestReviewShowDependents(t *testing.T) {
	f := newFakeAPI(t, reviewRoutes())
	out, _, code := runCLI(t, f, "review", "show", "g1", "aaaaaaaa")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"verdict_needed", "judge escalated", "n1 (alpha)", "dependents", "dependent: n3 (gamma)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "dependent: n2") {
		t.Errorf("prerequisite listed as a dependent:\n%s", out)
	}
	f.onlyReads(t)
}

func TestReviewNeverPosts(t *testing.T) {
	f := newFakeAPI(t, reviewRoutes())
	runCLI(t, f, "review", "g1", "--all")
	runCLI(t, f, "review", "show", "g1", "aaaaaaaa")
	for _, r := range f.requests() {
		if strings.Contains(r, "resolve") || !strings.HasPrefix(r, "GET ") {
			t.Fatalf("unexpected request %s", r)
		}
	}
}
