package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func init() {
	register("inbox", "list everything that needs a person: open review items and ready gates: inbox <graph> [--json] [--watch secs] [--kind review|gate]", runInbox)
	commandNotes["inbox"] = "  --watch secs\n    \tre-read every secs seconds (a whole number, 1 or more) and print only changes: + for a new item, - for one that is gone; not valid with --json\n" +
		"  --kind review|gate\n    \tlist only open review items or only ready gates; any other value exits 2\n"
	register("resolve", "answer a review item (a write): resolve <graph> <item> approve|reject|override [--note text]", runResolve)
	commandFlags["resolve"] = func(fs *flag.FlagSet) {
		fs.String("note", "", "why; required for override")
	}
}

const (
	inboxUsage   = "usage: graphwatch inbox <graph> [--json] [--watch secs] [--kind review|gate]"
	resolveUsage = "usage: graphwatch resolve <graph> <item> approve|reject|override [--note text]"
)

// inboxEntry is one line of the inbox, shaped for both the table and --json.
type inboxEntry struct {
	Type    string   `json:"type"`                // "review" or "gate"
	ID      string   `json:"id"`                  // review item id, or gate node id
	Kind    string   `json:"kind"`                // same as Type: "review" or "gate"
	Item    string   `json:"item_kind,omitempty"` // the review item's own kind, e.g. verdict_pending
	Node    string   `json:"node"`
	Title   string   `json:"title,omitempty"`
	Detail  string   `json:"detail,omitempty"`
	Options []string `json:"options,omitempty"`
	Age     string   `json:"age"`
}

// verdictDetail summarises the vote tally and the weakest criterion when the
// item carries them, and says nothing when it does not.
func verdictDetail(v *apiVerdictRef) string {
	if v == nil {
		return ""
	}
	var parts []string
	if v.Validation != nil && v.Validation.Tally != nil {
		t := v.Validation.Tally
		parts = append(parts, fmt.Sprintf("votes %d+ %d- %d?", t.Accept, t.Reject, t.Undecided))
	}
	if len(v.Criteria) > 0 {
		w := 0
		for i, c := range v.Criteria {
			if c.Noul < v.Criteria[w].Noul {
				w = i
			}
		}
		parts = append(parts, fmt.Sprintf("weakest %.2f %q", v.Criteria[w].Noul, truncate(v.Criteria[w].Text, 24)))
	}
	return strings.Join(parts, "; ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "..."
}

// collectInbox reads the open review items and the ready gates once. attend
// calls it on every refresh, so the listing and the follower cannot disagree.
func collectInbox(ctx context.Context, c *client, graphID string) ([]inboxEntry, error) {
	items, err := c.reviewItems(ctx, graphID)
	if err != nil {
		return nil, err
	}
	nodes, err := c.nodesFull(ctx, graphID)
	if err != nil {
		return nil, err
	}
	edges, err := c.edges(ctx, graphID)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	for _, n := range nodes {
		keys[n.ID] = n.Key
	}
	var entries []inboxEntry
	for _, it := range items {
		if !isOpenReview(it.State) {
			continue
		}
		key := it.NodeKey
		if key == "" {
			key = keys[it.NodeID]
		}
		detail := verdictDetail(it.Verdict)
		if detail == "" {
			detail = it.Reason
		}
		entries = append(entries, inboxEntry{Type: "review", Kind: "review", ID: it.ID, Item: it.Kind, Node: key, Detail: detail, Age: age(it.CreatedAt)})
	}
	for _, g := range gatesAwaitingDecision(nodes, edges) {
		entries = append(entries, inboxEntry{Type: "gate", Kind: "gate", ID: g.ID, Node: g.Key, Title: g.Title, Options: gateOptions(g), Age: "-"})
	}
	return entries, nil
}

func runInbox(ctx context.Context, env *cliEnv, args []string) int {
	// --json is a global flag; accept it after the graph argument too.
	var pos []string
	watch := time.Duration(0)
	kind := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--json" || a == "-json" {
			env.json = true
			continue
		}
		name, val, hasVal := strings.Cut(a, "=")
		if name == "--kind" || name == "-kind" {
			if !hasVal {
				if i+1 >= len(args) {
					fmt.Fprintln(env.errOut, "--kind must be review or gate")
					return exitUsage
				}
				i++
				val = args[i]
			}
			if val != "review" && val != "gate" {
				fmt.Fprintln(env.errOut, "--kind must be review or gate")
				return exitUsage
			}
			kind = val
			continue
		}
		if name == "--watch" || name == "-watch" {
			if !hasVal {
				if i+1 >= len(args) {
					fmt.Fprintln(env.errOut, "graphwatch: --watch needs a number of seconds")
					return exitUsage
				}
				i++
				val = args[i]
			}
			secs, err := strconv.Atoi(val)
			if err != nil || secs < 1 {
				fmt.Fprintf(env.errOut, "graphwatch: --watch wants a whole number of seconds, 1 or more, got %q\n", val)
				return exitUsage
			}
			watch = time.Duration(secs) * time.Second
			continue
		}
		pos = append(pos, a)
	}
	if watch > 0 && env.json {
		fmt.Fprintln(env.errOut, "graphwatch: --watch prints text lines and cannot be combined with --json")
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintln(env.errOut, inboxUsage)
		return exitUsage
	}
	graphID := pos[0]
	entries, err := collectInbox(ctx, env.client, graphID)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusNotFound {
			fmt.Fprintf(env.errOut, "graphwatch: no graph %s\n", graphID)
			return exitRuntime
		}
		return reportError(env.errOut, err)
	}
	entries = filterKind(entries, kind)
	if env.limit > 0 && len(entries) > env.limit {
		entries = entries[:env.limit]
	}
	if env.json {
		if entries == nil {
			entries = []inboxEntry{}
		}
		if err := renderJSON(env.out, entries); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	if len(entries) == 0 {
		fmt.Fprintln(env.out, "nothing needs you")
		if watch > 0 {
			return watchInbox(ctx, env, graphID, kind, entries, watch)
		}
		return exitOK
	}
	rows := make([][]string, len(entries))
	for i, e := range entries {
		rows[i] = append([]string{fmt.Sprint(i + 1)}, inboxFields(e)...)
	}
	env.table([]string{"#", "KIND", "ID", "NODE", "AGE", "DETAIL"}, rows)
	fmt.Fprintln(env.out, "\nanswer a review item: graphwatch resolve <graph> <item> approve|reject|override\ndecide a gate:        graphwatch decide <graph> <node> <decision>")
	if watch > 0 {
		return watchInbox(ctx, env, graphID, kind, entries, watch)
	}
	return exitOK
}

// filterKind keeps only entries of one kind; an empty kind keeps everything.
func filterKind(entries []inboxEntry, kind string) []inboxEntry {
	if kind == "" {
		return entries
	}
	var out []inboxEntry
	for _, e := range entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// inboxFields is the KIND, ID, NODE, AGE and DETAIL columns of one entry.
func inboxFields(e inboxEntry) []string {
	detail := e.Detail
	if e.Type == "gate" {
		detail = e.Title
		if len(e.Options) > 0 {
			detail += " [" + strings.Join(e.Options, " | ") + "]"
		}
	}
	id := e.ID
	if e.Type == "review" {
		id = reviewShortID(id)
	}
	col := e.Kind
	if e.Item != "" {
		col = e.Item
	}
	return []string{clean(col), clean(id), clean(e.Node), e.Age, clean(detail)}
}

// watchInbox follows the inbox after the first listing: every interval it
// re-reads and prints only what changed. It returns exitOK when ctx ends
// (Ctrl-C or SIGTERM), because stopping a watch is how it is meant to end.
func watchInbox(ctx context.Context, env *cliEnv, graphID, kind string, first []inboxEntry, interval time.Duration) int {
	t := time.NewTicker(interval)
	defer t.Stop()
	fetch := func(ctx context.Context) ([]inboxEntry, error) {
		entries, err := collectInbox(ctx, env.client, graphID)
		entries = filterKind(entries, kind)
		if env.limit > 0 && len(entries) > env.limit {
			entries = entries[:env.limit]
		}
		return entries, err
	}
	followInbox(ctx, env.out, env.errOut, first, fetch, t.C, time.Now)
	return exitOK
}

// followInbox is the loop behind watchInbox, with the ticker, the clock and
// the fetch injected so a test drives it without waiting. Items are matched by
// type and id, not by their printed line, because the AGE column changes every
// minute and would otherwise read as one item leaving and another arriving.
func followInbox(ctx context.Context, out, errOut io.Writer, prev []inboxEntry, fetch func(context.Context) ([]inboxEntry, error), ticks <-chan time.Time, now func() time.Time) {
	identity := func(e inboxEntry) string { return e.Type + " " + e.ID }
	line := func(e inboxEntry) string { return strings.Join(inboxFields(e), "  ") }
	known := map[string]inboxEntry{}
	for _, e := range prev {
		known[identity(e)] = e
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
		cur, err := fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// A failed refresh is not a change; say so and try again next tick.
			fmt.Fprintf(errOut, "%s refresh failed: %v\n", now().Format("15:04:05"), err)
			continue
		}
		stamp := now().Format("15:04:05")
		next := map[string]inboxEntry{}
		var buf strings.Builder
		rang := false
		for _, e := range cur {
			next[identity(e)] = e
			if _, ok := known[identity(e)]; !ok {
				fmt.Fprintf(&buf, "%s + %s\n", stamp, line(e))
				rang = true
			}
		}
		for _, e := range prev {
			if _, ok := next[identity(e)]; !ok {
				fmt.Fprintf(&buf, "%s - %s\n", stamp, line(e))
			}
		}
		if rang {
			buf.WriteString("\a")
		}
		io.WriteString(out, buf.String())
		known, prev = next, cur
	}
}

func runResolve(ctx context.Context, env *cliEnv, args []string) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	fs.SetOutput(env.errOut)
	note := fs.String("note", "", "")
	if f := env.fs.Lookup("note"); f != nil && f.Value.String() != "" {
		*note = f.Value.String()
	}
	// Flags may follow the positional arguments.
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return exitUsage
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	if len(pos) != 3 {
		fmt.Fprintln(env.errOut, resolveUsage)
		return exitUsage
	}
	graphID, want, action := pos[0], pos[1], pos[2]
	switch action {
	case "approve", "reject", "override":
	default:
		fmt.Fprintf(env.errOut, "graphwatch: unknown action %q\n%s\n", action, resolveUsage)
		return exitUsage
	}
	if action == "override" && strings.TrimSpace(*note) == "" {
		fmt.Fprintln(env.errOut, "graphwatch: override needs a reason: pass --note text")
		return exitUsage
	}

	items, err := env.client.reviewItems(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	var found *apiReviewItem
	for i := range items {
		if items[i].ID == want || (len(want) >= 4 && strings.HasPrefix(items[i].ID, want)) {
			if found != nil {
				fmt.Fprintf(env.errOut, "graphwatch: item prefix %q is ambiguous\n", want)
				return exitUsage
			}
			found = &items[i]
		}
	}
	if found == nil {
		fmt.Fprintf(env.errOut, "graphwatch: no review item %q in graph %s\n", want, graphID)
		return exitRuntime
	}

	resp, err := env.client.resolveReview(ctx, graphID, found.ID, resolveRequest{Action: action, Note: *note})
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			switch {
			case ae.status == 403:
				fmt.Fprintln(env.errOut, "you are not an arbitrator on this graph")
				return exitRuntime
			case ae.status == 409 && ae.code == "validation_not_decided":
				msg := ae.detail
				if msg == "" {
					msg = ae.msg
				}
				fmt.Fprintln(env.errOut, msg)
				return exitRuntime
			}
		}
		return reportError(env.errOut, err)
	}

	// The resolution does not list what it released, so read the node and the
	// nodes that require it back and show what the service now holds.
	nodes, nerr := env.client.nodesFull(ctx, graphID)
	edges, eerr := env.client.edges(ctx, graphID)
	byID := map[string]apiNodeFull{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	var deps []apiNodeFull
	if nerr == nil && eerr == nil {
		// A requires edge points from the dependent to its prerequisite.
		for _, e := range edges {
			if e.Type == "requires" && e.ToNodeID == found.NodeID {
				if n, ok := byID[e.FromNodeID]; ok {
					deps = append(deps, n)
				}
			}
		}
	}
	held := byID[found.NodeID]
	if env.json {
		out := map[string]any{"item": found.ID, "action": resp.Data.Action, "node": held.Key, "status": held.Status, "dependents": depSummary(deps)}
		if err := renderJSON(env.out, out); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	fmt.Fprintf(env.out, "%s %s on %s: node status %s\n", clean(found.Kind), action, clean(held.Key), clean(orUnknown(held.Status)))
	fmt.Fprintf(env.out, "dependents: %d\n", len(deps))
	for _, d := range deps {
		fmt.Fprintf(env.out, "  %s  %s  %s\n", clean(d.Key), clean(orUnknown(d.Status)), clean(d.WorkState))
	}
	return exitOK
}

func depSummary(deps []apiNodeFull) []map[string]string {
	out := make([]map[string]string, 0, len(deps))
	for _, d := range deps {
		out = append(out, map[string]string{"key": d.Key, "status": d.Status, "work_state": d.WorkState})
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
