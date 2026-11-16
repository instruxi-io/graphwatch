package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// showRoutes is one node, served both as the list and as the single-node read,
// plus an empty run list. extra is merged onto the node.
func showRoutes(extra map[string]any) map[string]any {
	n := map[string]any{
		"id": "n1", "key": "beta", "type": "task", "title": "Second",
		"status": "active", "work_state": "looking_for_work",
		"description": "Do the thing",
	}
	for k, v := range extra {
		n[k] = v
	}
	return map[string]any{
		"/graphs/g1/nodes":         []map[string]any{n},
		"/graphs/g1/nodes/n1":      n,
		"/graphs/g1/nodes/n1/runs": []map[string]any{},
	}
}

func TestNodesShowAssignmentGolden(t *testing.T) {
	cases := []struct {
		name   string
		extra  map[string]any
		golden string
	}{
		{
			name: "advisory",
			extra: map[string]any{
				"assignment_mode":  "advisory",
				"assignee":         map[string]any{"account_id": "ada"},
				"private":          false,
				"viewer_can_claim": true,
				"claim_block":      nil,
			},
			golden: "nodes_show_advisory.txt",
		},
		{
			name: "required account",
			extra: map[string]any{
				"assignment_mode":  "required",
				"assignee":         map[string]any{"account_id": "ada"},
				"private":          false,
				"viewer_can_claim": false,
				"claim_block":      "node_reserved",
			},
			golden: "nodes_show_required_account.txt",
		},
		{
			name: "required group",
			extra: map[string]any{
				"assignment_mode":  "required",
				"assignee":         map[string]any{"group_slug": "release-team"},
				"private":          false,
				"viewer_can_claim": false,
				"claim_block":      "node_reserved",
			},
			golden: "nodes_show_required_group.txt",
		},
		{
			name: "private",
			extra: map[string]any{
				"assignment_mode":  "advisory",
				"assignee":         map[string]any{"account_id": "ada"},
				"private":          true,
				"viewer_can_claim": true,
				"claim_block":      nil,
			},
			golden: "nodes_show_private.txt",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeAPI(t, showRoutes(c.extra))
			out, errOut, code := runCLI(t, f, "nodes", "show", "g1", "beta")
			if code != 0 {
				t.Fatalf("code %d\n%s", code, errOut)
			}
			if strings.Contains(out, "\x1b") {
				t.Errorf("colour on a pipe:\n%s", out)
			}
			checkGolden(t, c.golden, out)
			f.onlyReads(t)
		})
	}
}

func TestNodesAccessColumnGolden(t *testing.T) {
	// ACCESS is the server's claim_block and private, not a recomputed
	// membership. A required node this caller may claim stays "-".
	nodes := []map[string]any{
		{"id": "n1", "key": "alpha", "type": "task", "title": "Mine", "status": "active", "work_state": "looking_for_work",
			"assignment_mode": "required", "assignee": map[string]any{"account_id": "ada"},
			"private": false, "viewer_can_claim": true, "claim_block": nil,
			"data": map[string]any{"tier": "standard"}},
		{"id": "n2", "key": "beta", "type": "task", "title": "Theirs", "status": "active", "work_state": "looking_for_work",
			"assignment_mode": "required", "assignee": map[string]any{"account_id": "ada"},
			"private": false, "viewer_can_claim": false, "claim_block": "node_reserved"},
		{"id": "n3", "key": "gamma", "type": "task", "title": "Local", "status": "active", "work_state": "looking_for_work",
			"assignment_mode": "advisory", "private": true, "viewer_can_claim": true, "claim_block": nil},
		{"id": "n4", "key": "delta", "type": "task", "title": "Both", "status": "active", "work_state": "looking_for_work",
			"assignment_mode": "required", "assignee": map[string]any{"group_slug": "release-team"},
			"private": true, "viewer_can_claim": false, "claim_block": "node_reserved"},
	}
	f := newFakeAPI(t, map[string]any{"/graphs/g1/nodes": nodes})
	out, errOut, code := runCLI(t, f, "nodes", "g1")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, errOut)
	}
	if strings.ContainsAny(out, "🔒🏠") || strings.Contains(out, "\x1b") {
		t.Errorf("ACCESS must be plain ASCII with no colour on a pipe:\n%s", out)
	}
	checkGolden(t, "nodes_list_access.txt", out)
	f.onlyReads(t)
}

func TestNodesJSONPassesAssignmentThrough(t *testing.T) {
	node := map[string]any{
		"id": "n1", "key": "beta", "type": "task", "title": "Second", "status": "active",
		"work_state": "looking_for_work", "assignment_mode": "required",
		"assignee":         map[string]any{"account_id": "ada", "group_slug": nil},
		"private":          true,
		"viewer_can_claim": false,
		"claim_block":      "node_reserved",
	}
	f := newFakeAPI(t, map[string]any{
		"/graphs/g1/nodes":         []map[string]any{node},
		"/graphs/g1/nodes/n1":      node,
		"/graphs/g1/nodes/n1/runs": []any{},
	})
	out, _, code := runCLI(t, f, "nodes", "--json", "g1")
	if code != 0 {
		t.Fatal(code)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rows %d", len(got))
	}
	if got[0]["assignment_mode"] != "required" || got[0]["private"] != true || got[0]["viewer_can_claim"] != false || got[0]["claim_block"] != "node_reserved" {
		t.Fatalf("fields rewritten: %#v", got[0])
	}
	assignee, _ := got[0]["assignee"].(map[string]any)
	if assignee["account_id"] != "ada" {
		t.Fatalf("assignee rewritten: %#v", got[0]["assignee"])
	}

	show, _, code := runCLI(t, f, "nodes", "show", "--json", "g1", "beta")
	if code != 0 {
		t.Fatal(code)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(show), &envelope); err != nil {
		t.Fatal(err)
	}
	raw, _ := envelope["node"].(map[string]any)
	if raw["claim_block"] != "node_reserved" || raw["private"] != true || raw["assignment_mode"] != "required" {
		t.Fatalf("show json: %#v", raw)
	}
}

func TestStringAssigneeStillPrints(t *testing.T) {
	// An older server sent assignee as a string. It must still show, and a
	// missing assignment_mode is the server's default, advisory.
	f := newFakeAPI(t, showRoutes(map[string]any{"assignee": "ann"}))
	out, _, code := runCLI(t, f, "nodes", "show", "g1", "beta")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"assignee:    ann\n", "assignment:  advisory\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "private:") {
		t.Errorf("private printed when false:\n%s", out)
	}
}

func TestClaimReservedStderrGolden(t *testing.T) {
	reserved := map[string]any{"error": "node_reserved", "detail": "this node is reserved for its assignee"}
	cases := []struct {
		name, golden string
		extra        map[string]any
	}{
		{"named", "claim_node_reserved.txt", map[string]any{
			"assignment_mode": "required", "assignee": map[string]any{"account_id": "ada"},
			"claim_block": "node_reserved", "viewer_can_claim": false,
		}},
		{"no name", "claim_node_reserved_unnamed.txt", map[string]any{
			"assignment_mode": "required", "claim_block": "node_reserved", "viewer_can_claim": false,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeAPI(t, showRoutes(c.extra))
			f.posts = map[string]fakeReply{"/graphs/g1/nodes/n1/claim": {409, reserved}}
			out, errOut, code := runCLI(t, f, "nodes", "claim", "g1", "beta")
			if code != exitRuntime {
				t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
			}
			if out != "" {
				t.Errorf("stdout %q", out)
			}
			checkGolden(t, c.golden, errOut)
			var posted bool
			for _, r := range f.requests() {
				if r == "POST /api/v1/graph/graphs/g1/nodes/n1/claim" {
					posted = true
				}
			}
			if !posted {
				t.Fatalf("claim was not posted: %v", f.requests())
			}
		})
	}
}

func TestDecideAcceptsAssigneeObject(t *testing.T) {
	// enforcer-graph sends assignee as an object. Decoding it as a string
	// failed the whole node read, so decide never posted.
	routes := gateRoutes()
	nodes := routes["/graphs/g1/nodes"].([]map[string]any)
	nodes[1]["assignee"] = map[string]any{"account_id": "ada", "group_slug": "release-team"}
	nodes[1]["assignment_mode"] = "required"
	nodes[1]["viewer_can_claim"] = true
	nodes[1]["claim_block"] = nil
	f := newFakeAPI(t, routes)
	f.posts = map[string]fakeReply{"/graphs/g1/nodes/n2/decide": {200, map[string]any{
		"success": true, "frontier": []map[string]any{},
	}}}
	out, errOut, code := runCLI(t, f, "decide", "g1", "ship-gate", "ship")
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("decide did not post: %v", f.requests())
	}
}

func TestClaimSucceeds(t *testing.T) {
	f := newFakeAPI(t, showRoutes(map[string]any{"assignment_mode": "advisory", "viewer_can_claim": true}))
	f.posts = map[string]fakeReply{"/graphs/g1/nodes/n1/claim": {200, map[string]any{
		"success": true,
		"data":    map[string]any{"key": "beta", "run_id": "run-1", "assignment_mode": "advisory", "viewer_can_claim": true, "claim_block": nil},
	}}}
	out, errOut, code := runCLI(t, f, "nodes", "claim", "g1", "beta")
	if code != 0 {
		t.Fatalf("code %d %s", code, errOut)
	}
	if !strings.Contains(out, "claimed beta") || !strings.Contains(out, "run run-1") {
		t.Fatalf("stdout %q", out)
	}
}
