package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
)

// The watch view answers what needs you: the same items `attend` lists, the
// same writes `attend` makes (sendAnswer), and never without an explicit
// keystroke followed by a y/N confirm that shows exactly what will be sent.
// --read-only removes the answer keys and the writer altogether.

// watchReadOnly is the --read-only flag; watchNoBell is --no-bell.
var (
	watchReadOnly bool
	watchNoBell   bool
)

// needsYou holds the latest inbox read for the header badge, the panel and
// the bell. seen is every item id this session has announced, so an item
// rings once when it first appears and never again on a refresh.
type needsYou struct {
	mu    sync.Mutex
	items []inboxEntry
	known bool
	seen  map[string]bool
}

func needsKey(e inboxEntry) string { return e.Type + ":" + e.ID }

// update stores a fresh read and returns how many of its items this session
// has not seen before. An item that leaves and comes back is not new.
func (n *needsYou) update(entries []inboxEntry) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen == nil {
		n.seen = map[string]bool{}
	}
	fresh := 0
	for _, e := range entries {
		if k := needsKey(e); !n.seen[k] {
			n.seen[k] = true
			fresh++
		}
	}
	n.items, n.known = entries, true
	return fresh
}

// snapshot returns the items and whether any read has succeeded yet.
func (n *needsYou) snapshot() ([]inboxEntry, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]inboxEntry(nil), n.items...), n.known
}

// startNeeds re-reads the inbox through collectInbox (attend's source) on the
// shared poll and, through the returned poke, on stream events. ring is called
// once per newly arrived item; nil rings nothing (--no-bell).
func startNeeds(ctx context.Context, c *client, graphID string, n *needsYou, tick <-chan struct{}, ring func()) (poke func()) {
	kick := make(chan struct{}, 1)
	go func() {
		for {
			if entries, err := collectInbox(ctx, c, graphID); err == nil {
				for i := n.update(entries); i > 0 && ring != nil; i-- {
					ring()
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-kick:
			case <-tick:
			}
		}
	}()
	return func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
}

// askMode is where the answer flow is.
type askMode int

const (
	askClosed  askMode = iota // the graph has the keys
	askPanel                  // the needs-you list is open
	askPick                   // choosing a decision or an action
	askText                   // typing a note (or a merge target / reply text)
	askConfirm                // y sends, anything else cancels
)

// askState is the answer flow's whole state. It is a value: askUpdate returns
// the next one, so every transition is testable without a terminal.
type askState struct {
	mode     askMode
	readOnly bool
	graph    string
	cursor   int        // panel row
	item     inboxEntry // the item being answered
	choices  []string
	choice   int
	text     string
}

// askEffect is what a key asks the loop to do beyond the state change.
type askEffect struct {
	pass  bool    // not consumed: graph navigation and quit see the key
	jump  string  // select this node key in the graph
	write *answer // a confirmed answer to send
	say   string  // a status line
}

// choicesFor is what can be picked for an item: a gate's own options, else
// approve/reject; a review item's actions as attend shows them.
func choicesFor(e inboxEntry) []string {
	if e.Type == "gate" {
		if len(e.Options) > 0 {
			return e.Options
		}
		return []string{"approve", "reject"}
	}
	if a := actionsForKind(e.Item); len(a) > 0 {
		return a
	}
	return []string{"approve", "reject"}
}

// textPrompt names what the text step asks for, and whether it may be empty.
func textPrompt(action string) (string, bool) {
	switch action {
	case "merge":
		return "observation id to merge into", false
	case "reply":
		return "reply text", false
	case "override":
		return "note (required)", false
	}
	return "note (optional, enter to skip)", true
}

// startAnswer opens the pick step for e, or says why it cannot.
func startAnswer(s askState, e inboxEntry) (askState, askEffect) {
	s.mode, s.item, s.choices, s.choice, s.text = askPick, e, choicesFor(e), 0, ""
	return s, askEffect{}
}

// itemsOnNode is the needs-you items held by one node key.
func itemsOnNode(items []inboxEntry, key string) []inboxEntry {
	var out []inboxEntry
	for _, e := range items {
		if key != "" && e.Node == key {
			out = append(out, e)
		}
	}
	return out
}

// askUpdate applies one key. items is the current needs-you list and selKey
// the graph's selected node key. Pure: no I/O, no clock. ctrl-c always passes
// so the loop can restore the terminal and leave.
func askUpdate(s askState, items []inboxEntry, selKey, k string) (askState, askEffect) {
	if k == "ctrl-c" {
		return s, askEffect{pass: true}
	}
	switch s.mode {
	case askClosed:
		switch k {
		case "n":
			s.mode, s.cursor = askPanel, 0
			return s, askEffect{}
		case "a":
			if s.readOnly {
				return s, askEffect{pass: true}
			}
			on := itemsOnNode(items, selKey)
			if len(on) == 0 {
				return s, askEffect{say: "nothing on the selected node needs you; n lists what does"}
			}
			return startAnswer(s, on[0])
		}
		return s, askEffect{pass: true}
	case askPanel:
		switch k {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(items)-1 {
				s.cursor++
			}
		case "enter":
			if s.cursor < len(items) {
				s.mode = askClosed
				return s, askEffect{jump: items[s.cursor].Node}
			}
		case "a":
			if !s.readOnly && s.cursor < len(items) {
				return startAnswer(s, items[s.cursor])
			}
		case "n", "esc":
			s.mode = askClosed
		case "q":
			return s, askEffect{pass: true}
		}
		return s, askEffect{}
	case askPick:
		switch k {
		case "up", "k", "left", "h":
			if s.choice > 0 {
				s.choice--
			}
		case "down", "j", "right", "l":
			if s.choice < len(s.choices)-1 {
				s.choice++
			}
		case "enter":
			s.mode, s.text = askText, ""
		case "esc":
			return cancelAnswer(s)
		default:
			if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'1') < len(s.choices) {
				s.choice = int(k[0] - '1')
				s.mode, s.text = askText, ""
			}
		}
		return s, askEffect{}
	case askText:
		switch k {
		case "enter":
			label, optional := textPrompt(s.choices[s.choice])
			if !optional && strings.TrimSpace(s.text) == "" {
				return s, askEffect{say: label + " is needed"}
			}
			s.mode = askConfirm
		case "esc":
			return cancelAnswer(s)
		case "backspace":
			if r := []rune(s.text); len(r) > 0 {
				s.text = string(r[:len(r)-1])
			}
		default:
			if len([]rune(k)) == 1 && len(s.text) < 4000 {
				s.text += k
			}
		}
		return s, askEffect{}
	case askConfirm:
		if k == "y" || k == "Y" {
			a := s.pending()
			s.mode = askClosed
			return s, askEffect{write: &a, say: "sending " + a.Action + " on " + clean(a.Item.Node) + "..."}
		}
		return cancelAnswer(s)
	}
	return s, askEffect{pass: true}
}

func cancelAnswer(s askState) (askState, askEffect) {
	s.mode, s.text = askClosed, ""
	return s, askEffect{say: "cancelled: nothing was written"}
}

// pending is the answer the confirm step shows and y sends.
func (s askState) pending() answer {
	action := s.choices[s.choice]
	a := answer{Graph: s.graph, Item: s.item, Action: action}
	switch action {
	case "merge", "reply":
		a.Value = strings.TrimSpace(s.text)
	default:
		a.Note = strings.TrimSpace(s.text)
	}
	return a
}

// askLines is the panel the flow draws over the bottom rows, or nil when it
// is closed. Pure.
func askLines(s askState, items []inboxEntry, width int) []string {
	var out []string
	switch s.mode {
	case askClosed:
		return nil
	case askPanel:
		hint := "↑↓ select · enter go to node · n/esc close"
		if !s.readOnly {
			hint = "↑↓ select · enter go to node · a answer · n/esc close"
		}
		out = append(out, fmt.Sprintf("needs you: %d   %s", len(items), hint))
		if len(items) == 0 {
			out = append(out, "  nothing needs you")
		}
		for i, e := range items {
			mark := "  "
			if i == s.cursor {
				mark = "› "
			}
			out = append(out, mark+strings.Join(inboxFields(e), "  "))
		}
	case askPick:
		out = append(out, "answer "+itemLabel(s.item)+"   ↑↓ or 1-9 choose · enter next · esc cancel")
		for i, c := range s.choices {
			mark := "  "
			if i == s.choice {
				mark = "› "
			}
			out = append(out, fmt.Sprintf("%s%d %s", mark, i+1, clean(c)))
		}
	case askText:
		label, _ := textPrompt(s.choices[s.choice])
		out = append(out, "answer "+itemLabel(s.item)+": "+clean(s.choices[s.choice])+"   enter next · esc cancel")
		out = append(out, label+": "+s.text+"▏")
	case askConfirm:
		a := s.pending()
		out = append(out, "confirm write: y sends it, any other key cancels")
		out = append(out, "  graph     "+clean(a.Graph))
		if a.Item.Type == "gate" {
			out = append(out, "  gate      "+clean(a.Item.Node)+"  ("+clean(a.Item.ID)+")")
			out = append(out, "  decision  "+clean(a.Action))
		} else {
			out = append(out, "  item      "+clean(a.Item.Item)+" "+clean(a.Item.ID)+" on "+clean(a.Item.Node))
			out = append(out, "  action    "+clean(a.Action))
			switch a.Action {
			case "merge":
				out = append(out, "  into      "+clean(a.Value))
			case "reply":
				out = append(out, "  text      "+clean(a.Value))
			}
		}
		note := a.Note
		if note == "" {
			note = "(none)"
		}
		out = append(out, "  note      "+clean(note))
	}
	for i := range out {
		out[i] = clip(out[i], width)
	}
	return out
}

func itemLabel(e inboxEntry) string {
	if e.Type == "gate" {
		return "gate " + clean(e.Node)
	}
	return clean(e.Item) + " on " + clean(e.Node)
}

// asker connects the pure flow to the running view: it owns the state, reads
// the needs-you list and runs a confirmed write off the frame loop.
type asker struct {
	mu    sync.Mutex
	st    askState
	needs *needsYou
	// submit sends one confirmed answer and returns the line to show. nil in
	// read-only mode, so no key sequence can reach a write.
	submit  func(answer) string
	say     func(string)
	jump    func(key string)
	refresh func()
	// run starts a write; `go` in the view, inline in tests.
	run func(func())
}

// key applies one key and reports whether the flow consumed it.
func (a *asker) key(k, selKey string) bool {
	items, _ := a.needs.snapshot()
	a.mu.Lock()
	st, eff := askUpdate(a.st, items, selKey, k)
	a.st = st
	a.mu.Unlock()
	if eff.say != "" && a.say != nil {
		a.say(eff.say)
	}
	if eff.jump != "" && a.jump != nil {
		a.jump(eff.jump)
	}
	if eff.write != nil && a.submit != nil {
		w := *eff.write
		run := a.run
		if run == nil {
			run = func(f func()) { go f() }
		}
		run(func() {
			line := a.submit(w)
			if a.say != nil {
				a.say(line)
			}
			if a.refresh != nil {
				a.refresh()
			}
		})
	}
	return !eff.pass
}

func (a *asker) lines(width int) []string {
	items, _ := a.needs.snapshot()
	a.mu.Lock()
	st := a.st
	a.mu.Unlock()
	return askLines(st, items, width)
}

// answerSubmitter sends through sendAnswer with its printed outcome captured,
// and returns that outcome as one status line: the service's answer, or its
// refusal.
func answerSubmitter(ctx context.Context, c *client) func(answer) string {
	return func(a answer) string {
		var out, errb bytes.Buffer
		env := &cliEnv{client: c, out: &out, errOut: &errb, fs: flag.NewFlagSet("watch", flag.ContinueOnError)}
		code := sendAnswer(ctx, env, a)
		text := out.String()
		if code != exitOK || strings.TrimSpace(text) == "" {
			text = errb.String() + text
		}
		return oneLine(text)
	}
}

// oneLine joins a command's printed lines into one status line.
func oneLine(s string) string {
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " · ")
}

// bellWriter rings the terminal bell on w; BEL draws nothing, so the frame
// diff is unaffected.
func bellWriter(w io.Writer) func() { return func() { io.WriteString(w, "\a") } }

// newWatchAsker builds the view's answer flow. In read-only mode it has no
// submitter, so even a confirmed answer goes nowhere.
func newWatchAsker(ctx context.Context, c *client, graphID string, v *view, needs *needsYou, refresh func()) *asker {
	a := &asker{
		st:      askState{graph: graphID, readOnly: watchReadOnly},
		needs:   needs,
		refresh: refresh,
		say: func(s string) {
			v.mu.Lock()
			v.say(clean(s), rgb{250, 190, 60})
			v.mu.Unlock()
		},
		jump: v.selectKey,
	}
	if !watchReadOnly {
		a.submit = answerSubmitter(ctx, c)
	}
	return a
}

// selectedKey is the key of the inspector's selected node, or "".
func (v *view) selectedKey() string {
	if v.insp == nil {
		return ""
	}
	v.insp.mu.Lock()
	id := v.insp.sel
	v.insp.mu.Unlock()
	v.mu.Lock()
	defer v.mu.Unlock()
	if g := v.nodes[id]; g != nil {
		return g.key
	}
	return ""
}

// selectKey moves the inspector's selection to the node with this key.
func (v *view) selectKey(key string) {
	v.mu.Lock()
	id := v.byKey[key]
	v.mu.Unlock()
	if id == "" || v.insp == nil {
		return
	}
	v.insp.mu.Lock()
	v.insp.sel = id
	if v.insp.open {
		v.insp.fetchLocked(id, false)
	}
	v.insp.mu.Unlock()
}
