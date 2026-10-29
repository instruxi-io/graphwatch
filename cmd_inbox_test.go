package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func inboxRoutes() map[string]any {
	ts := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	return map[string]any{
		"/graphs/g1/review": []map[string]any{
			{"id": "run:aaaaaaaa-1111", "kind": "verdict_pending", "node_id": "n1", "node_key": "alpha", "run_id": "r1", "state": "open", "reason": "escalated", "created_at": ts,
				"verdict": map[string]any{
					"criteria":   []map[string]any{{"text": "tests pass", "noul": 0.9}, {"text": "docs updated", "noul": 0.41}},
					"validation": map[string]any{"tally": map[string]any{"accept": 1, "reject": 1, "undecided": 0}},
				}},
			{"id": "run:bbbbbbbb-2222", "kind": "verdict_pending", "node_id": "n9", "node_key": "old", "state": "resolved", "created_at": ts},
		},
		"/graphs/g1/nodes": []map[string]any{
			{"id": "n1", "key": "alpha", "type": "task", "status": "verifying"},
			{"id": "n2", "key": "ship-gate", "type": "gate", "status": "active", "title": "Ship it?", "data": map[string]any{"options": []string{"ship", "hold"}}},
			{"id": "n3", "key": "later-gate", "type": "gate", "status": "active", "title": "Not yet"},
			{"id": "n4", "key": "build", "type": "task", "status": "active"},
			{"id": "n5", "key": "roll", "type": "task", "status": "pending", "work_state": "ready"},
		},
		"/graphs/g1/edges": []map[string]any{
			{"from_node_id": "n3", "to_node_id": "n4", "type": "requires"},
			{"from_node_id": "n5", "to_node_id": "n1", "type": "requires"},
		},
	}
}

func TestInboxListsReviewItemsAndReadyGates(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	out, _, code := runCLI(t, f, "inbox", "g1")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	for _, want := range []string{"verdict_pending", "run:aaaa", "alpha", "2h", "votes 1+ 1- 0?", "weakest 0.41", "docs updated", "ship-gate", "Ship it?", "ship | hold"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "alpha") > strings.Index(out, "ship-gate") {
		t.Errorf("review item should come before the gate:\n%s", out)
	}
	for _, bad := range []string{"later-gate", "run:bbbb"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q listed:\n%s", bad, out)
		}
	}
	f.onlyReads(t)

	jout, _, code := runCLI(t, f, "inbox", "g1", "--json")
	var entries []inboxEntry
	if code != 0 || json.Unmarshal([]byte(jout), &entries) != nil || len(entries) != 2 {
		t.Fatalf("json code %d:\n%s", code, jout)
	}
}

func TestInboxEmpty(t *testing.T) {
	f := newFakeAPI(t, map[string]any{
		"/graphs/g1/review": []map[string]any{},
		"/graphs/g1/nodes":  []map[string]any{},
		"/graphs/g1/edges":  []map[string]any{},
	})
	out, _, code := runCLI(t, f, "inbox", "g1")
	if code != 0 || !strings.Contains(out, "nothing needs you") {
		t.Fatalf("code %d:\n%s", code, out)
	}
}

func TestResolvePostsActionAndNote(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	f.posts = map[string]fakeReply{"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {200, map[string]any{
		"success": true, "data": map[string]any{"action": "approve", "item_id": "run:aaaaaaaa-1111", "kind": "verdict_pending"},
	}}}
	out, _, code := runCLI(t, f, "resolve", "g1", "run:aaaa", "approve", "--note", "looked fine")
	if code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if len(f.bodies) != 1 {
		t.Fatalf("bodies %v", f.bodies)
	}
	var got resolveRequest
	if err := json.Unmarshal([]byte(f.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Action != "approve" || got.Note != "looked fine" {
		t.Errorf("body %+v", got)
	}
	for _, want := range []string{"approve on alpha", "dependents: 1", "roll"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestResolveRefusals(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	_, errOut, code := runCLI(t, f, "resolve", "g1", "run:aaaa", "override")
	if code != 2 || !strings.Contains(errOut, "--note") {
		t.Errorf("override without note: code %d, %q", code, errOut)
	}
	if len(f.bodies) != 0 {
		t.Errorf("override without a note reached the server: %v", f.bodies)
	}

	f.posts = map[string]fakeReply{"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {403, map[string]any{
		"success": false, "error": map[string]any{"code": "forbidden", "message": "no"}}}}
	_, errOut, code = runCLI(t, f, "resolve", "g1", "run:aaaa", "approve")
	if code != 1 || !strings.Contains(errOut, "you are not an arbitrator on this graph") {
		t.Errorf("403: code %d, %q", code, errOut)
	}

	f.posts = map[string]fakeReply{"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {409, map[string]any{
		"success": false, "error": map[string]any{"code": "validation_not_decided", "message": "2 of 3 judges have not voted"}}}}
	_, errOut, code = runCLI(t, f, "resolve", "g1", "run:aaaa", "approve")
	if code != 1 || !strings.Contains(errOut, "2 of 3 judges have not voted") {
		t.Errorf("409: code %d, %q", code, errOut)
	}
}

func watchEntry(id, node string) inboxEntry {
	return inboxEntry{Type: "review", ID: id, Kind: "verdict_pending", Node: node, Detail: "escalated", Age: "2h"}
}

// followOnce drives followInbox through one tick per fetch result, with a
// fixed clock, and returns what it printed.
func followOnce(t *testing.T, first []inboxEntry, passes ...[]inboxEntry) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	i := 0
	fetch := func(context.Context) ([]inboxEntry, error) {
		// The extra tick below repeats the last pass, which changes nothing.
		cur := passes[min(i, len(passes)-1)]
		i++
		return cur, nil
	}
	clock := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local) }
	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		followInbox(ctx, &out, io.Discard, first, fetch, ticks, clock)
		close(done)
	}()
	for range passes {
		ticks <- time.Time{}
	}
	// An unbuffered send is taken only after the previous pass has printed.
	ticks <- time.Time{}
	cancel()
	<-done
	return out.String()
}

func TestInboxWatchPrintsOnlyChanges(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	out, _, code := runCLI(t, f, "inbox", "g1")
	if code != 0 || !strings.Contains(out, "alpha") || !strings.Contains(out, "ship-gate") {
		t.Fatalf("first listing, code %d:\n%s", code, out)
	}
	a, b := watchEntry("run:aaaa", "alpha"), watchEntry("run:cccc", "beta")
	// The second pass has the same item with an older age: no change.
	aged := a
	aged.Age = "3h"
	got := followOnce(t, []inboxEntry{a}, []inboxEntry{aged}, []inboxEntry{aged, b})
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(got, "\a", ""), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "03:04:05 + ") || !strings.Contains(lines[0], "beta") {
		t.Fatalf("want exactly one + line for beta, got %q", got)
	}
	if quiet := followOnce(t, []inboxEntry{a}, []inboxEntry{a}); quiet != "" {
		t.Fatalf("nothing changed but printed %q", quiet)
	}
}

func TestInboxWatchNewItemRings(t *testing.T) {
	a, b := watchEntry("run:aaaa", "alpha"), watchEntry("run:cccc", "beta")
	if got := followOnce(t, []inboxEntry{a}, []inboxEntry{a, b}); !strings.Contains(got, "\a") {
		t.Fatalf("new item should ring the bell, got %q", got)
	}
	if got := followOnce(t, []inboxEntry{a, b}, []inboxEntry{a}); strings.Contains(got, "\a") {
		t.Fatalf("a removed item should not ring, got %q", got)
	}
}

func TestInboxWatchRemovedItemPrintsMinus(t *testing.T) {
	a, b := watchEntry("run:aaaa", "alpha"), watchEntry("run:cccc", "beta")
	got := followOnce(t, []inboxEntry{a, b}, []inboxEntry{a})
	if !strings.HasPrefix(got, "03:04:05 - ") || strings.Count(got, "\n") != 1 || !strings.Contains(got, "beta") {
		t.Fatalf("want one - line for beta, got %q", got)
	}
}

func TestInboxWatchRejectsBadValue(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	for _, v := range []string{"0", "abc", "-3"} {
		if _, errOut, code := runCLI(t, f, "inbox", "g1", "--watch", v); code != 2 || !strings.Contains(errOut, "--watch") {
			t.Errorf("--watch %s: code %d, stderr %q", v, code, errOut)
		}
	}
}

func TestInboxKindGateHidesReviewItems(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	out, _, code := runCLI(t, f, "inbox", "g1", "--kind", "gate")
	if code != 0 || !strings.Contains(out, "ship-gate") || strings.Contains(out, "alpha") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	out, _, code = runCLI(t, f, "inbox", "g1", "--kind=review")
	if code != 0 || !strings.Contains(out, "alpha") || strings.Contains(out, "ship-gate") {
		t.Fatalf("code %d:\n%s", code, out)
	}
}

func TestInboxKindBadValueExitsTwo(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	out, errOut, code := runCLI(t, f, "inbox", "g1", "--kind", "bogus")
	if code != 2 || !strings.Contains(errOut, "--kind must be review or gate") || out != "" {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
}

func TestInboxJSONCarriesKind(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	jout, _, code := runCLI(t, f, "inbox", "g1", "--json")
	var items []map[string]any
	if code != 0 || json.Unmarshal([]byte(jout), &items) != nil || len(items) != 2 {
		t.Fatalf("code %d:\n%s", code, jout)
	}
	if items[0]["kind"] != "review" || items[1]["kind"] != "gate" {
		t.Errorf("kinds %v %v", items[0]["kind"], items[1]["kind"])
	}
	jout, _, _ = runCLI(t, f, "inbox", "g1", "--json", "--kind", "gate")
	items = nil
	if json.Unmarshal([]byte(jout), &items) != nil || len(items) != 1 || items[0]["kind"] != "gate" {
		t.Errorf("filtered json:\n%s", jout)
	}
}

// An empty inbox under --watch says so once, then prints only changes: a
// second empty read adds nothing, and an arrival prints one + line.
func TestInboxWatchEmptyPrintsNothingNeedsYouOnce(t *testing.T) {
	f := newFakeAPI(t, map[string]any{
		"/graphs/g1/review": []map[string]any{},
		"/graphs/g1/nodes":  []map[string]any{},
		"/graphs/g1/edges":  []map[string]any{},
	})
	var out, errb bytes.Buffer
	env := &cliEnv{client: newClient(f.URL, apiKey("test-key")), out: &out, errOut: &errb}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- runInbox(ctx, env, []string{"g1", "--watch", "1"}) }()
	// Let at least one 1s tick re-read the still-empty inbox before stopping.
	time.Sleep(1500 * time.Millisecond)
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb.String())
	}
	if n := strings.Count(out.String(), "nothing needs you"); n != 1 || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("want the empty line exactly once and nothing else, got %q", out.String())
	}
	got := followOnce(t, nil, nil, []inboxEntry{watchEntry("run:aaaa", "alpha")})
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, " + ") || strings.Contains(got, "nothing needs you") {
		t.Fatalf("want one + line after an empty start, got %q", got)
	}
}

func TestInboxUnknownGraphExitsOne(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	out, errOut, code := runCLI(t, f, "inbox", "nope")
	if code != 1 || !strings.Contains(errOut, "no graph nope") || out != "" {
		t.Fatalf("code %d, out %q, stderr %q", code, out, errOut)
	}
}

func TestInboxKindJSONIsValid(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	for _, kind := range []string{"review", "gate"} {
		jout, _, code := runCLI(t, f, "inbox", "g1", "--json", "--kind", kind)
		var items []inboxEntry
		if code != 0 || json.Unmarshal([]byte(jout), &items) != nil || len(items) != 1 || items[0].Kind != kind {
			t.Errorf("--kind %s: code %d:\n%s", kind, code, jout)
		}
	}
	// A kind with no matches is an empty array, not null.
	f2 := newFakeAPI(t, map[string]any{
		"/graphs/g1/review": []map[string]any{}, "/graphs/g1/nodes": []map[string]any{}, "/graphs/g1/edges": []map[string]any{},
	})
	jout, _, _ := runCLI(t, f2, "inbox", "g1", "--json", "--kind", "gate")
	if strings.TrimSpace(jout) != "[]" {
		t.Errorf("empty filtered json: %q", jout)
	}
}

func resolveOK(action, kind string) map[string]fakeReply {
	return map[string]fakeReply{"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {200, map[string]any{
		"success": true, "data": map[string]any{"action": action, "item_id": "run:aaaaaaaa-1111", "kind": kind},
	}}}
}

func lastResolveBody(t *testing.T, f *fakeAPI) resolveRequest {
	t.Helper()
	if len(f.bodies) != 1 {
		t.Fatalf("bodies %v", f.bodies)
	}
	var got resolveRequest
	if err := json.Unmarshal([]byte(f.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestResolvePassesRejudgeKeepAndMerge(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want resolveRequest
	}{
		{"rejudge on a judge hold", []string{"rejudge"}, resolveRequest{Action: "rejudge"}},
		{"keep on an observation", []string{"keep", "--note", "still true"}, resolveRequest{Action: "keep", Note: "still true"}},
		{"merge with into", []string{"merge", "--into", "obs-7"}, resolveRequest{Action: "merge", Into: "obs-7"}},
		{"reply with text", []string{"reply", "--text", "yes"}, resolveRequest{Action: "reply", Text: "yes"}},
		{"approve with profile", []string{"approve", "--profile", "p1"}, resolveRequest{Action: "approve", ProfileID: "p1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, inboxRoutes())
			f.posts = resolveOK(tc.args[0], "verdict_pending")
			args := append([]string{"resolve", "g1", "run:aaaa"}, tc.args...)
			out, errOut, code := runCLI(t, f, args...)
			if code != 0 {
				t.Fatalf("code %d:\n%s\n%s", code, out, errOut)
			}
			if got := lastResolveBody(t, f); got != tc.want {
				t.Errorf("body %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveServerInvalidActionPrintsKind(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	f.posts = map[string]fakeReply{"/graphs/g1/review/run:aaaaaaaa-1111/resolve": {400, map[string]any{
		"success": false, "error": map[string]any{"code": "invalid_action", "message": "dismiss is not valid here"}}}}
	_, errOut, code := runCLI(t, f, "resolve", "g1", "run:aaaa", "dismiss")
	if code != 1 || !strings.Contains(errOut, "invalid_action") || !strings.Contains(errOut, "dismiss is not valid here") || !strings.Contains(errOut, "verdict_pending") {
		t.Errorf("code %d, %q", code, errOut)
	}
}

func TestInboxShowsActionsPerKind(t *testing.T) {
	f := newFakeAPI(t, inboxRoutes())
	out, _, _ := runCLI(t, f, "inbox", "g1")
	if !strings.Contains(out, "ACTIONS") || !strings.Contains(out, "approve|reject|rejudge") {
		t.Errorf("no actions shown:\n%s", out)
	}
}
