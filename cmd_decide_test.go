package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func gateRoutes() map[string]any {
	return map[string]any{
		"/graphs/g1/review": []map[string]any{},
		"/graphs/g1/nodes": []map[string]any{
			{"id": "n1", "key": "build", "type": "task", "status": "done", "title": "Build"},
			{"id": "n2", "key": "ship-gate", "type": "gate", "status": "active", "title": "Ship it?", "data": map[string]any{"options": []string{"ship", "hold"}}},
			{"id": "n3", "key": "early-gate", "type": "gate", "status": "pending", "title": "Not yet"},
			{"id": "n4", "key": "late-gate", "type": "gate", "status": "failed", "title": "Behind work"},
			{"id": "n5", "key": "open-task", "type": "task", "status": "active", "title": "Still running"},
			{"id": "n6", "key": "closed-gate", "type": "gate", "status": "done", "title": "Decided"},
			{"id": "n7", "key": "roll-prod", "type": "task", "status": "pending", "title": "Roll prod"},
		},
		"/graphs/g1/edges": []map[string]any{
			{"from_node_id": "n2", "to_node_id": "n1", "type": "requires"},
			{"from_node_id": "n4", "to_node_id": "n5", "type": "requires"},
			{"from_node_id": "n7", "to_node_id": "n2", "type": "requires"},
		},
	}
}

func TestReviewListsGatesAwaitingDecision(t *testing.T) {
	f := newFakeAPI(t, gateRoutes())
	out, _, code := runCLI(t, f, "review", "g1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"Gates awaiting a decision", "ship-gate", "Ship it?", "ship | hold"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// Not decidable: prerequisite unfinished, not yet runnable, already done, not a gate.
	for _, bad := range []string{"late-gate", "early-gate", "closed-gate", "open-task"} {
		if strings.Contains(out, bad) {
			t.Errorf("%s listed:\n%s", bad, out)
		}
	}
	f.onlyReads(t)
}

func TestDecidePostsAndPrintsFrontier(t *testing.T) {
	routes := gateRoutes()
	f := newFakeAPI(t, routes)
	f.posts = map[string]fakeReply{"/graphs/g1/nodes/n2/decide": {200, map[string]any{
		"success":  true,
		"frontier": []map[string]any{{"node_id": "n7", "key": "roll-prod", "title": "Roll prod", "type": "task"}},
	}}}
	out, _, code := runCLI(t, f, "decide", "g1", "ship-gate", "ship", "--note", "smoke passed", "--follow-up", "roll-prod", "--follow-up", "announce")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("bodies %v", f.bodies)
	}
	var got decideRequest
	if err := json.Unmarshal([]byte(f.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Decision != "ship" || got.Note != "smoke passed" || strings.Join(got.FollowUps, ",") != "roll-prod,announce" {
		t.Errorf("body %+v", got)
	}
	for _, want := range []string{"ship-gate", "status", "released 1", "roll-prod  Roll prod"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestDecideRefusalsExitOne(t *testing.T) {
	cases := []struct {
		name, node string
		status     int
		body       map[string]any
		want       string
	}{
		{"not an arbitrator", "ship-gate", 403, map[string]any{"error": map[string]any{"code": "insufficient_scope", "message": "no"}}, "you are not an arbitrator on this graph"},
		{"not a gate", "open-task", 409, map[string]any{"error": map[string]any{"code": "not_a_gate", "message": "no"}}, "open-task is not a gate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeAPI(t, gateRoutes())
			f.posts = map[string]fakeReply{}
			for _, id := range []string{"n2", "n5"} {
				f.posts["/graphs/g1/nodes/"+id+"/decide"] = fakeReply{c.status, c.body}
			}
			_, errOut, code := runCLI(t, f, "decide", "g1", c.node, "ship")
			if code != 1 {
				t.Errorf("code %d", code)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("stderr %q lacks %q", errOut, c.want)
			}
		})
	}
}
