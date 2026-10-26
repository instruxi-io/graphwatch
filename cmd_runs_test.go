package main

import (
	"strings"
	"testing"
)

func runsRoutes() map[string]any {
	run := map[string]any{
		"id": "run-1", "node_id": "n1", "attempt": 2, "status": "succeeded", "runner": "worker-a",
		"started_at": "2026-10-07T10:00:00Z", "ended_at": "2026-10-07T10:05:00Z",
		"report": "1. MET: did it", "outputs": map[string]any{"pr": "http://x/1"},
		"evidence": []map[string]any{
			{"kind": "command", "cmd": "go test", "exit": 0, "output": strings.Repeat("line\n", 30)},
		},
		"verification": map[string]any{"state": "pending", "policy": "validation"},
		"validation":   map[string]any{"state": "pending", "tally": map[string]any{"accept": 1, "reject": 0, "undecided": 2}},
	}
	return map[string]any{
		"/graphs/g1/nodes/n1/runs": []map[string]any{run,
			{"id": "run-0", "attempt": 1, "status": "failed", "started_at": "2026-10-07T09:00:00Z"},
			{"id": "run-0b", "attempt": 3, "status": "failed", "started_at": "2026-10-07T09:30:00Z"}},
		"/graphs/g1/nodes":                         []map[string]any{{"id": "n1", "key": "alpha"}},
		"/graphs/g1/nodes/n1/runs/run-0/verdicts":  []map[string]any{},
		"/graphs/g1/nodes/n1/runs/run-0b/verdicts": []map[string]any{},
		"/graphs/g1/nodes/n1/runs/run-1/verdicts": []map[string]any{{
			"id": "verdict-1234", "state": "rejected", "model": "jev-1", "created_at": "2026-10-07T10:06:00Z",
			"verification": map[string]any{"state": "rejected", "policy": "gate", "confidence": 0.9, "reason": "unsupported_by_evidence",
				"criteria": []map[string]any{{"text": "tests pass", "noul": 0.93}, {"text": "docs updated", "noul": 0.08}}},
		}},
		"/graphs/g1/nodes/n1/runs/run-1/votes": []map[string]any{
			{"voter": "jev", "vote": "accept", "note": "looks right", "created_at": "2026-10-07T10:07:00Z"}},
	}
}

func TestRunsListTable(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	out, _, code := runCLI(t, f, "runs", "g1", "n1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"ATTEMPT", "STATUS", "RUNNER", "STARTED", "FINISHED", "VERDICT", "worker-a", "succeeded", "pending (validation pending)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestRunsShowVerdicts(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	out, _, code := runCLI(t, f, "runs", "show", "g1", "n1", "run-1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if first := strings.SplitN(out, "\n", 2)[0]; !strings.HasPrefix(first, "HELD verifying") {
		t.Errorf("first line not a held note: %q", first)
	}
	for _, want := range []string{"outcome rejected", "unsupported_by_evidence", "criterion 1: MET", "criterion 2: NOT MET", "policy gate", "1. MET: did it"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestRunsShowVotes(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	out, _, _ := runCLI(t, f, "runs", "show", "g1", "n1", "run-1")
	for _, want := range []string{"accept 1  reject 0  undecided 2", "votes (1)", "jev  accept", "looks right"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestRunsEvidenceTruncates(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	out, _, _ := runCLI(t, f, "runs", "show", "g1", "n1", "run-1")
	if n := strings.Count(out, "| line"); n != 12 || !strings.Contains(out, "18 more lines") {
		t.Errorf("want 12 lines and a note, got %d:\n%s", n, out)
	}
	out, _, _ = runCLI(t, f, "runs", "show", "g1", "n1", "run-1", "--full")
	if n := strings.Count(out, "| line"); n != 30 {
		t.Errorf("--full: want 30 lines, got %d", n)
	}
}

func TestRunsReadOnly(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	runCLI(t, f, "runs", "g1", "n1")
	runCLI(t, f, "runs", "show", "g1", "n1", "run-1")
	if len(f.requests()) < 4 {
		t.Fatalf("too few requests: %v", f.requests())
	}
	f.onlyReads(t)
}

func TestRunsShowResolvesPrefix(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	// The node by key and the run by a unique prefix.
	out, _, code := runCLI(t, f, "runs", "show", "g1", "alpha", "run-1")
	if code != 0 || !strings.Contains(out, "run run-1  attempt 2") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	out, _, code = runCLI(t, f, "runs", "show", "g1", "n1", "run-0b")
	if code != 0 || !strings.Contains(out, "run run-0b  attempt 3") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	_, errOut, code := runCLI(t, f, "runs", "show", "g1", "n1", "zzz")
	if code != 1 || !strings.Contains(errOut, "no run zzz on n1") {
		t.Fatalf("code %d: %s", code, errOut)
	}
}

func TestRunsShowAmbiguousPrefixExitsOne(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	_, errOut, code := runCLI(t, f, "runs", "show", "g1", "n1", "run-0")
	// run-0 is an exact id, so it resolves; run- matches all three.
	if code != 0 {
		t.Fatalf("exact id with longer sibling: code %d: %s", code, errOut)
	}
	_, errOut, code = runCLI(t, f, "runs", "show", "g1", "n1", "run-")
	if code != 1 || !strings.Contains(errOut, "ambiguous run id") {
		t.Fatalf("code %d: %s", code, errOut)
	}
}

func TestRunsShowFullPrintsVerdictsAndVotes(t *testing.T) {
	f := newFakeAPI(t, runsRoutes())
	out, _, code := runCLI(t, f, "runs", "show", "g1", "n1", "run-1", "--full")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"outcome rejected  model jev-1", "criterion 2: NOT MET (p=0.08)", "jev  accept", "looks right"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
