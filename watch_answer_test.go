package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var askItems = []inboxEntry{
	{Type: "review", Kind: "review", ID: "run:aaaaaaaa-1111", Item: "verdict_pending", Node: "alpha", Age: "2m"},
	{Type: "gate", Kind: "gate", ID: "n2", Node: "ship-gate", Title: "Ship?", Options: []string{"ship", "hold"}, Age: "-"},
}

// feed runs keys through askUpdate and collects every effect.
func feed(s askState, items []inboxEntry, sel string, keys ...string) (askState, []askEffect) {
	var effs []askEffect
	for _, k := range keys {
		var e askEffect
		s, e = askUpdate(s, items, sel, k)
		effs = append(effs, e)
	}
	return s, effs
}

func writes(effs []askEffect) []answer {
	var out []answer
	for _, e := range effs {
		if e.write != nil {
			out = append(out, *e.write)
		}
	}
	return out
}

func TestAskPanelOpensSelectsAndJumps(t *testing.T) {
	s := askState{graph: "g1"}
	s, effs := feed(s, askItems, "", "n")
	if s.mode != askPanel || effs[0].pass {
		t.Fatalf("n did not open the panel: %+v %+v", s, effs[0])
	}
	if l := askLines(s, askItems, 120); !strings.Contains(l[0], "needs you: 2") || !strings.HasPrefix(l[1], "› ") {
		t.Fatalf("panel lines %q", l)
	}
	s, effs = feed(s, askItems, "", "down", "down", "enter")
	if s.cursor != 1 || s.mode != askClosed || effs[2].jump != "ship-gate" {
		t.Fatalf("enter did not jump to the gate: %+v %+v", s, effs)
	}
	s, _ = feed(s, askItems, "", "n", "esc")
	if s.mode != askClosed {
		t.Fatalf("esc did not close: %+v", s)
	}
	s, effs = feed(s, askItems, "", "n", "q")
	if !effs[1].pass {
		t.Fatal("q in the panel must reach the loop to quit")
	}
	if _, effs = feed(askState{}, askItems, "", "esc", "q", "up", "enter"); effs[0].pass != true || !effs[1].pass || !effs[2].pass || !effs[3].pass {
		t.Fatalf("a closed flow must pass graph keys: %+v", effs)
	}
}

func TestAskGateDecisionConfirmsExactAnswer(t *testing.T) {
	s := askState{graph: "g1"}
	// a on the selected node that holds the gate, choose option 2, type a note.
	s, _ = feed(s, askItems, "ship-gate", "a")
	if s.mode != askPick || s.item.ID != "n2" || strings.Join(s.choices, "|") != "ship|hold" {
		t.Fatalf("pick step %+v", s)
	}
	keys := []string{"down", "enter", "n", "o", "t", " ", "y", "e", "x", "backspace", "t", "enter"}
	s, effs := feed(s, askItems, "ship-gate", keys...)
	if s.mode != askConfirm || len(writes(effs)) != 0 {
		t.Fatalf("not at confirm, or wrote early: %+v %v", s, writes(effs))
	}
	got := strings.Join(askLines(s, askItems, 120), "\n")
	for _, want := range []string{"confirm write", "graph     g1", "gate      ship-gate  (n2)", "decision  hold", "note      not yet"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirm screen lacks %q:\n%s", want, got)
		}
	}
	s, effs = feed(s, askItems, "ship-gate", "y")
	w := writes(effs)
	want := answer{Graph: "g1", Item: askItems[1], Action: "hold", Note: "not yet"}
	if len(w) != 1 || w[0].Graph != want.Graph || w[0].Item.ID != "n2" || w[0].Action != want.Action || w[0].Note != want.Note || s.mode != askClosed {
		t.Fatalf("y wrote %+v, want %+v", w, want)
	}
}

func TestAskAnythingButYCancels(t *testing.T) {
	for _, k := range []string{"n", "N", "esc", "enter", "q", " ", "x"} {
		s := askState{graph: "g1"}
		s, effs := feed(s, askItems, "", "n", "a", "enter", "enter", k)
		if len(writes(effs)) != 0 || s.mode != askClosed || !strings.Contains(effs[len(effs)-1].say, "cancelled") {
			t.Errorf("%q at confirm: mode %v writes %v say %q", k, s.mode, writes(effs), effs[len(effs)-1].say)
		}
	}
	// esc leaves the pick and text steps without writing too.
	for _, seq := range [][]string{{"n", "a", "esc"}, {"n", "a", "enter", "esc"}} {
		s, effs := feed(askState{}, askItems, "", seq...)
		if len(writes(effs)) != 0 || s.mode != askClosed {
			t.Errorf("%v: %+v", seq, s)
		}
	}
}

func TestAskReviewNeedsValueForOverrideMergeReply(t *testing.T) {
	item := []inboxEntry{{Type: "review", ID: "obs:1", Item: "observation_review", Node: "beta"}}
	s, effs := feed(askState{graph: "g1"}, item, "beta", "a", "2", "enter")
	if s.mode != askText || !strings.Contains(effs[2].say, "observation id") {
		t.Fatalf("merge without a target went on: %+v %+v", s, effs[2])
	}
	s, effs = feed(s, item, "beta", "o", "7", "enter", "y")
	w := writes(effs)
	if len(w) != 1 || w[0].Action != "merge" || w[0].Value != "o7" || w[0].Note != "" {
		t.Fatalf("merge wrote %+v", w)
	}
	// ctrl-c passes in every step so the loop can restore the terminal.
	for _, m := range []askMode{askClosed, askPanel, askPick, askText, askConfirm} {
		if _, e := askUpdate(askState{mode: m, choices: []string{"approve"}}, item, "", "ctrl-c"); !e.pass {
			t.Errorf("ctrl-c consumed in mode %v", m)
		}
	}
}

func TestAskReadOnlyHasNoAnswerPath(t *testing.T) {
	s := askState{graph: "g1", readOnly: true}
	seqs := [][]string{
		{"a", "enter", "enter", "y"},
		{"n", "a", "enter", "enter", "y"},
		{"n", "down", "a", "1", "enter", "y"},
	}
	for _, seq := range seqs {
		got, effs := feed(s, askItems, "ship-gate", seq...)
		if len(writes(effs)) != 0 || got.mode == askPick || got.mode == askConfirm {
			t.Errorf("read-only %v reached %v, wrote %v", seq, got.mode, writes(effs))
		}
	}
	if _, e := askUpdate(s, askItems, "ship-gate", "a"); !e.pass {
		t.Error("read-only must not consume the answer key")
	}
	ro, _ := feed(s, askItems, "", "n")
	if l := askLines(ro, askItems, 120); strings.Contains(l[0], "answer") {
		t.Errorf("read-only panel offers answering: %q", l[0])
	}
}

// The asker in read-only mode, wired to the real client, issues only GETs
// whatever is typed.
func TestWatchReadOnlyNeverCallsAWrite(t *testing.T) {
	f := attendFake(t)
	watchReadOnly = true
	t.Cleanup(func() { watchReadOnly = false })
	c := newClient(f.URL, apiKey("test-key"))
	ctx := context.Background()
	needs := &needsYou{}
	entries, err := collectInbox(ctx, c, "g1")
	if err != nil {
		t.Fatal(err)
	}
	needs.update(entries)
	v := newView()
	v.insp = newInspector()
	a := newWatchAsker(ctx, c, "g1", v, needs, nil)
	a.run = func(f func()) { f() }
	if a.submit != nil {
		t.Fatal("read-only asker has a submitter")
	}
	for _, k := range strings.Split("n a enter 1 enter y y n down a 2 enter y", " ") {
		a.key(k, "ship-gate")
	}
	// Even a confirmed answer forced into the state goes nowhere.
	a.st = askState{mode: askConfirm, graph: "g1", item: askItems[1], choices: []string{"ship"}}
	a.key("y", "")
	f.onlyReads(t)
	if len(f.bodies) != 0 {
		t.Fatalf("posted %v", f.bodies)
	}
}

// y sends through the same calls attend makes, with exactly what the
// confirm screen showed; any other key sends nothing.
func TestWatchConfirmCallsDecideAndResolve(t *testing.T) {
	f := attendFake(t)
	c := newClient(f.URL, apiKey("test-key"))
	ctx := context.Background()
	needs := &needsYou{}
	entries, err := collectInbox(ctx, c, "g1")
	if err != nil {
		t.Fatal(err)
	}
	needs.update(entries)
	var said []string
	refreshed := 0
	a := &asker{st: askState{graph: "g1"}, needs: needs, submit: answerSubmitter(ctx, c),
		say: func(s string) { said = append(said, s) }, refresh: func() { refreshed++ }, run: func(f func()) { f() }}
	press := func(sel string, keys ...string) {
		for _, k := range keys {
			a.key(k, sel)
		}
	}

	press("ship-gate", "a", "1", "o", "k", "enter", "n") // cancelled at confirm
	press("alpha", "a", "enter", "enter", "esc")         // cancelled at confirm
	if len(f.bodies) != 0 {
		t.Fatalf("a cancelled answer was posted: %v", f.bodies)
	}

	press("ship-gate", "a", "1", "g", "o", "enter", "y")
	var d decideRequest
	if len(f.bodies) != 1 || json.Unmarshal([]byte(f.bodies[0]), &d) != nil || d.Decision != "ship" || d.Note != "go" {
		t.Fatalf("decide bodies %v", f.bodies)
	}
	press("alpha", "a", "enter", "l", "g", "t", "m", "enter", "y")
	var r resolveRequest
	if len(f.bodies) != 2 || json.Unmarshal([]byte(f.bodies[1]), &r) != nil || r.Action != "approve" || r.Note != "lgtm" {
		t.Fatalf("resolve bodies %v", f.bodies)
	}
	var posts []string
	for _, q := range f.requests() {
		if strings.HasPrefix(q, "POST ") {
			posts = append(posts, q)
		}
	}
	if strings.Join(posts, "\n") != "POST /api/v1/graph/graphs/g1/nodes/n2/decide\nPOST /api/v1/graph/graphs/g1/review/run:aaaaaaaa-1111/resolve" {
		t.Fatalf("posts %v", posts)
	}
	all := strings.Join(said, "\n")
	if !strings.Contains(all, `ship-gate decided "ship"`) || !strings.Contains(all, "approve on alpha") || refreshed != 2 {
		t.Fatalf("status lines %q, refreshed %d", said, refreshed)
	}
}

func TestNeedsBellOncePerNewItem(t *testing.T) {
	n := &needsYou{}
	a, b, c := askItems[0], askItems[1], inboxEntry{Type: "review", ID: "obs:3", Item: "contradiction", Node: "gamma"}
	steps := []struct {
		read []inboxEntry
		want int
	}{
		{[]inboxEntry{a, b}, 2},
		{[]inboxEntry{a, b}, 0}, // a refresh with nothing new
		{[]inboxEntry{a, b, c}, 1},
		{[]inboxEntry{c}, 0},
		{[]inboxEntry{a, c}, 0}, // an item that comes back is not new
		{nil, 0},
	}
	for i, s := range steps {
		if got := n.update(s.read); got != s.want {
			t.Errorf("step %d: %d new, want %d", i, got, s.want)
		}
	}
}

// startNeeds rings once per new item across repeated reads of the same inbox.
func TestStartNeedsRingsOnlyForNewIDs(t *testing.T) {
	f := attendFake(t)
	c := newClient(f.URL, apiKey("test-key"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var rings atomic.Int32
	tick := make(chan struct{})
	n := &needsYou{}
	poke := startNeeds(ctx, c, "g1", n, tick, func() { rings.Add(1) })
	wait := func(reads int) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			count := 0
			for _, q := range f.requests() {
				if strings.Contains(q, "/review") {
					count++
				}
			}
			if count >= reads {
				time.Sleep(20 * time.Millisecond)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("no read %d", reads)
	}
	wait(1)
	poke()
	wait(2)
	tick <- struct{}{}
	wait(3)
	if got := rings.Load(); got != 2 {
		t.Fatalf("rang %d times over three reads of two items, want 2", got)
	}
	if items, ok := n.snapshot(); !ok || len(items) != 2 {
		t.Fatalf("snapshot %v %v", items, ok)
	}
}
