package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

func init() {
	register("review", "list the human review items holding a graph: review <graph> [--open|--all] | review show <graph> <item>", runReview)
}

const reviewUsage = "usage: graphwatch review <graph> [--open|--all]\n       graphwatch review show <graph> <item>"

// isOpenReview: anything not plainly finished counts as open, so a state the
// viewer has not heard of is shown rather than hidden.
func isOpenReview(state string) bool {
	switch strings.ToLower(state) {
	case "resolved", "closed", "dismissed", "done":
		return false
	}
	return true
}

func reviewShortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func init() {
	// Declared on the shared set so `help review` lists them; runReview parses
	// them again because they may follow the graph argument.
	commandFlags["review"] = func(fs *flag.FlagSet) {
		fs.Bool("open", false, "only open items (the default)")
		fs.Bool("all", false, "open and resolved items")
	}
}

func runReview(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) > 0 && args[0] == "show" {
		return runReviewShow(ctx, env, args[1:])
	}
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(env.errOut)
	open := fs.Bool("open", false, "only open items (the default)")
	all := fs.Bool("all", false, "open and resolved items")
	// Flags may follow the graph argument.
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
	*open, *all = *open || flagSet(env, "open"), *all || flagSet(env, "all")
	if len(pos) != 1 || (*open && *all) {
		fmt.Fprintln(env.errOut, reviewUsage)
		return exitUsage
	}
	items, err := env.client.reviewItems(ctx, pos[0])
	if err != nil {
		return reportError(env.errOut, err)
	}
	var shown []apiReviewItem
	for _, it := range items {
		if *all || isOpenReview(it.State) {
			shown = append(shown, it)
		}
	}
	if env.limit > 0 && len(shown) > env.limit {
		shown = shown[:env.limit]
	}
	if env.json {
		raws := make([]json.RawMessage, len(shown))
		for i, it := range shown {
			raws[i] = it.Raw
		}
		if err := renderJSON(env.out, raws); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	rows := make([][]string, len(shown))
	for i, it := range shown {
		rows[i] = []string{reviewShortID(it.ID), it.Kind, reviewShortID(it.NodeID), reviewShortID(it.RunID), it.State, age(it.CreatedAt), it.Reason}
	}
	env.table([]string{"ITEM", "KIND", "NODE", "RUN", "STATE", "AGE", "REASON"}, rows)
	return printGatesAwaiting(ctx, env, pos[0])
}

// gatesAwaitingDecision returns the gate nodes a person can decide now: status
// active, failed or needs_review, with every prerequisite done. A requires edge
// points from the dependent to its prerequisite, so a node's prerequisites are
// the To side of the edges that start at it.
func gatesAwaitingDecision(nodes []apiNodeFull, edges []apiEdge) []apiNodeFull {
	status := map[string]string{}
	for _, n := range nodes {
		status[n.ID] = n.Status
	}
	blocked := map[string]bool{}
	for _, e := range edges {
		if e.Type == "requires" && status[e.ToNodeID] != "done" {
			blocked[e.FromNodeID] = true
		}
	}
	var out []apiNodeFull
	for _, n := range nodes {
		if n.Type != "gate" || blocked[n.ID] {
			continue
		}
		switch n.Status {
		case "active", "failed", "needs_review":
			out = append(out, n)
		}
	}
	return out
}

// gateOptions reads the decision options a gate declares in data.options.
func gateOptions(n apiNodeFull) []string {
	var d struct {
		Options []string `json:"options"`
	}
	if len(n.Data) == 0 || json.Unmarshal(n.Data, &d) != nil {
		return nil
	}
	return d.Options
}

// printGatesAwaiting adds the gate block under the item table. JSON output is
// left as the item list, so a script reading it sees no new shape.
func printGatesAwaiting(ctx context.Context, env *cliEnv, graphID string) int {
	if env.json {
		return exitOK
	}
	nodes, err := env.client.nodesFull(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	edges, err := env.client.edges(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	gates := gatesAwaitingDecision(nodes, edges)
	if len(gates) == 0 {
		return exitOK
	}
	fmt.Fprintln(env.out, "\nGates awaiting a decision")
	rows := make([][]string, len(gates))
	for i, g := range gates {
		opts := strings.Join(gateOptions(g), " | ")
		if opts == "" {
			opts = "-"
		}
		rows[i] = []string{g.Key, g.Title, g.Status, opts}
	}
	env.table([]string{"KEY", "TITLE", "STATUS", "OPTIONS"}, rows)
	return exitOK
}

func runReviewShow(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(env.errOut, reviewUsage)
		return exitUsage
	}
	graphID, want := args[0], args[1]
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
	nodes, err := env.client.nodes(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	edges, err := env.client.edges(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	keys := map[string]string{}
	for _, n := range nodes {
		keys[n.ID] = n.Key
	}
	// A requires edge points from the dependent to its prerequisite, so the
	// nodes that wait on the held node are the From side of edges ending at it.
	var deps []string
	for _, e := range edges {
		if e.Type == "requires" && e.ToNodeID == found.NodeID {
			deps = append(deps, e.FromNodeID)
		}
	}
	if env.json {
		out := map[string]any{"item": found.Raw, "dependents": deps}
		if err := renderJSON(env.out, out); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	w := env.out
	fmt.Fprintf(w, "item:   %s\nkind:   %s\nstate:  %s\nage:    %s\n", found.ID, found.Kind, found.State, age(found.CreatedAt))
	fmt.Fprintf(w, "node:   %s\n", labelled(found.NodeID, keys))
	fmt.Fprintf(w, "run:    %s\n", found.RunID)
	fmt.Fprintf(w, "reason: %s\n\n", clean(found.Reason))
	fmt.Fprintf(w, "dependents (unblocked when this item is resolved): %d\n", len(deps))
	printDependents(w, deps, keys)
	return exitOK
}

func labelled(id string, keys map[string]string) string {
	if k := keys[id]; k != "" {
		return clean(id) + " (" + clean(k) + ")"
	}
	return clean(id)
}

func printDependents(w io.Writer, deps []string, keys map[string]string) {
	for _, d := range deps {
		fmt.Fprintf(w, "  dependent: %s\n", labelled(d, keys))
	}
}
