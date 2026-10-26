package main

import (
	"strings"
	"testing"
)

const gid = "11111111-2222-3333-4444-555555555555"
const acct = "c3d4e5f6-0000-4000-8000-000000000002"

func accessFake(t *testing.T) *fakeAPI {
	return newFakeAPI(t, map[string]any{
		"/graphs/" + gid + "/access": map[string]any{"accounts": []any{
			map[string]any{"account_id": acct, "username": "ada", "role": "admin",
				"via": []any{map[string]any{"kind": "creator"}}},
			map[string]any{"account_id": "99999999-0000-4000-8000-000000000001", "role": "viewer",
				"via": []any{map[string]any{"kind": "group", "group_slug": "release-reviewers"}}},
		}},
	})
}

func TestAccessTable(t *testing.T) {
	f := accessFake(t)
	out, _, code := runCLI(t, f, "access", gid)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, w := range []string{"ACCOUNT", "ROLE", "VIA", "owner", "group:release-reviewers", "ada"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
	if strings.Contains(out, acct) {
		t.Errorf("full id printed without --full-ids:\n%s", out)
	}
	out, _, _ = runCLI(t, f, "access", gid, "--full-ids")
	if !strings.Contains(out, acct) {
		t.Errorf("--full-ids did not print the id:\n%s", out)
	}
}

func TestAccessNotFound(t *testing.T) {
	f := newFakeAPI(t, nil)
	_, errOut, code := runCLI(t, f, "access", gid)
	if code == 0 || !strings.Contains(errOut, "graph not found") {
		t.Errorf("code %d stderr %q", code, errOut)
	}
}

func epochFake(t *testing.T) *fakeAPI {
	return newFakeAPI(t, map[string]any{
		"/graphs/" + gid + "/epochs": []any{
			map[string]any{"epoch": 1, "started_at": "2026-01-01T00:00:00Z", "ended_at": "2026-01-02T00:00:00Z",
				"start_reason": "created", "stats": map[string]any{"nodes": 5, "done": 4, "failed": 1}},
			map[string]any{"epoch": 2, "current": true, "started_at": "2026-01-02T00:00:00Z", "ended_at": nil,
				"start_reason": "manual", "stats": map[string]any{"nodes": 5, "done": 2, "failed": 0}},
		},
		"/graphs/" + gid + "/epochs/2": map[string]any{"epoch": 2, "current": true,
			"started_at": "2026-01-02T00:00:00Z", "start_reason": "manual",
			"stats": map[string]any{"nodes": 5, "done": 2}},
	})
}

func TestEpochsMarksCurrent(t *testing.T) {
	out, _, code := runCLI(t, epochFake(t), "epochs", gid)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "EPOCH") {
		t.Fatalf("table:\n%s", out)
	}
	if !strings.Contains(lines[2], "2 *") || strings.Contains(lines[1], "*") {
		t.Errorf("current not marked only on epoch 2:\n%s", out)
	}
}

func TestEpochsShow(t *testing.T) {
	out, _, code := runCLI(t, epochFake(t), "epochs", "show", gid, "2")
	if code != 0 || !strings.Contains(out, "manual") || !strings.Contains(out, "2 *") {
		t.Errorf("code %d\n%s", code, out)
	}
}

func TestAccessAndEpochsReadOnly(t *testing.T) {
	f := epochFake(t)
	runCLI(t, f, "epochs", gid)
	runCLI(t, f, "epochs", "show", gid, "2")
	g := accessFake(t)
	runCLI(t, g, "access", gid)
	if len(f.requests()) == 0 || len(g.requests()) == 0 {
		t.Fatal("no requests recorded")
	}
	f.onlyReads(t)
	g.onlyReads(t)
}
