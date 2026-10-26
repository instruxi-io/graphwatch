package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func attendFake(t *testing.T) *fakeAPI {
	f := newFakeAPI(t, inboxRoutes())
	f.posts = map[string]fakeReply{
		"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {200, map[string]any{
			"success": true, "data": map[string]any{"action": "approve", "item_id": "run:aaaaaaaa-1111", "kind": "verdict_pending"}}},
		"/graphs/g1/nodes/n2/decide": {200, map[string]any{
			"success": true, "data": map[string]any{"frontier": []map[string]any{{"node_id": "n6", "key": "release", "title": "Release", "type": "task"}}}}},
	}
	return f
}

func runAttendScript(t *testing.T, f *fakeAPI, script string) (string, string, int) {
	t.Helper()
	attendIn = strings.NewReader(script)
	t.Cleanup(func() { attendIn = nil })
	return runCLI(t, f, "attend", "g1")
}

func TestAttendPostsApproveFromStdin(t *testing.T) {
	f := attendFake(t)
	out, _, code := runAttendScript(t, f, "1 approve looks good\nquit\n")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	for _, want := range []string{"1  verdict_pending  alpha", "2  gate  ship-gate", "\a2 need you", "approve on alpha"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if len(f.bodies) != 1 {
		t.Fatalf("bodies %v", f.bodies)
	}
	var got resolveRequest
	if err := json.Unmarshal([]byte(f.bodies[0]), &got); err != nil || got.Action != "approve" || got.Note != "looks good" {
		t.Fatalf("body %s (%v)", f.bodies[0], err)
	}
}

func TestAttendDecidesGateFromStdin(t *testing.T) {
	f := attendFake(t)
	out, _, code := runAttendScript(t, f, "2 decide ship go now\nquit\n")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if !strings.Contains(out, `ship-gate decided "ship"`) || !strings.Contains(out, "release") {
		t.Errorf("outcome missing:\n%s", out)
	}
	var got decideRequest
	if len(f.bodies) != 1 || json.Unmarshal([]byte(f.bodies[0]), &got) != nil || got.Decision != "ship" || got.Note != "go now" {
		t.Fatalf("bodies %v", f.bodies)
	}
}

func TestAttendRefusesDecisionOutsideOptions(t *testing.T) {
	f := attendFake(t)
	out, errOut, code := runAttendScript(t, f, "2 decide maybe\n9 approve\nquit\n")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if len(f.bodies) != 0 {
		t.Fatalf("a refused decision was posted: %v", f.bodies)
	}
	if !strings.Contains(errOut, "ship | hold") || !strings.Contains(errOut, "no item 9") {
		t.Errorf("stderr:\n%s", errOut)
	}
}

func TestAttendExitsOnEOF(t *testing.T) {
	f := attendFake(t)
	out, _, code := runAttendScript(t, f, "")
	if code != 0 || !strings.Contains(out, "2 need you") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 0 {
		t.Fatalf("EOF wrote: %v", f.bodies)
	}
}

// An item that arrives while you answer another is announced by the read that
// follows the answer, without typing list.
func TestAttendAnnouncesItemArrivingDuringAnswer(t *testing.T) {
	routes := inboxRoutes()
	f := newFakeAPI(t, routes)
	f.posts = map[string]fakeReply{
		"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {200, map[string]any{
			"success": true, "data": map[string]any{"action": "approve"}}},
	}
	f.afterPost = func() {
		f.setRoute(routes, "/graphs/g1/review", []map[string]any{
			{"id": "run:cccccccc-3333", "kind": "verdict_pending", "node_id": "n4", "node_key": "newcomer", "state": "open", "reason": "late arrival", "created_at": time.Now().Format(time.RFC3339)},
		})
	}
	out, _, code := runAttendScript(t, f, "1 approve\nquit\n")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	i := strings.Index(out, "approve on alpha")
	if i < 0 {
		t.Fatalf("no answer line:\n%s", out)
	}
	after := out[i:]
	if !strings.Contains(after, "3  verdict_pending  newcomer") || !strings.Contains(after, "\a") {
		t.Errorf("the item that arrived during the answer was not announced with a bell:\n%s", after)
	}
}

const escapeTitle = "\x1b]0;PWNED\x07\x1b[2J"

// Text from the API is written by other accounts: no raw control byte may
// reach the terminal, in a table, a follower line or a detail view.
func TestPrintedStringsAreSanitised(t *testing.T) {
	routes := inboxRoutes()
	routes["/graphs/g1/nodes"] = []map[string]any{
		{"id": "n1", "key": "alpha", "type": "task", "status": "verifying"},
		{"id": "n2", "key": "ship-gate", "type": "gate", "status": "active", "title": escapeTitle, "data": map[string]any{"options": []string{"ship", "hold"}}},
	}
	routes["/graphs/g1/edges"] = []map[string]any{}
	routes["/graphs/g1/recall"] = []any{
		map[string]any{"kind": "note", "node_key": "build", "created_at": "2026-10-01T10:00:00Z", "run_id": "r1", "body": escapeTitle},
	}
	f := newFakeAPI(t, routes)
	for _, args := range [][]string{{"inbox", "g1"}, {"recall", "g1"}, {"review", "g1"}, {"review", "show", "g1", "aaaa"}} {
		out, errOut, _ := runCLI(t, f, args...)
		for _, o := range []string{out, errOut} {
			if strings.ContainsAny(o, "\x1b\x07") {
				t.Errorf("%v printed a raw control byte: %q", args, o)
			}
		}
		if args[0] != "review" && !strings.Contains(out, `\x1b]0;PWNED\x07\x1b[2J`) {
			t.Errorf("%v: escaped title not shown: %q", args, out)
		}
	}
	attendIn = strings.NewReader("list\nquit\n")
	t.Cleanup(func() { attendIn = nil })
	out, _, _ := runCLI(t, f, "attend", "g1")
	if strings.ContainsAny(out, "\x1b") || !strings.Contains(out, `\x1b]0;PWNED`) {
		t.Errorf("attend printed %q", out)
	}
	if got := clean("a\tb\nc\u009bd\x7f"); got != "a\tb\nc\\u009bd\\x7f" {
		t.Errorf("clean = %q", got)
	}
}

// Flags may follow the graph, as the usage line shows them.
func TestAttendFlagsAfterGraphWork(t *testing.T) {
	f := attendFake(t)
	attendIn = strings.NewReader("")
	t.Cleanup(func() { attendIn = nil })
	out, errOut, code := runCLI(t, f, "attend", "g1", "--poll", "1")
	if code != 0 {
		t.Fatalf("code %d\nout:\n%s\nerr:\n%s", code, out, errOut)
	}
	attendIn = strings.NewReader("")
	if _, errOut, code = runCLI(t, f, "inbox", "g1", "--limit", "1", "--no-color"); code != 0 {
		t.Fatalf("inbox flags after the graph: code %d\n%s", code, errOut)
	}
}
