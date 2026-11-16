package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
)

func init() {
	register("nodes", "list a graph's tasks, show one, or claim one: nodes <graph> [--status s] [--type t] | nodes show <graph> <key|id> | nodes claim <graph> <key|id>", runNodes)
	commandFlags["nodes"] = func(fs *flag.FlagSet) {
		fs.String("status", "", "only nodes with this status")
		fs.String("type", "", "only nodes of this type")
	}
}

const nodesUsage = "usage: graphwatch nodes <graph> [--status s] [--type t]\n       graphwatch nodes show <graph> <key|id>\n       graphwatch nodes claim <graph> <key|id>"

// nodeData reads the node's free-form data object; a missing or odd one is empty.
func nodeData(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func nodeDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// positionals collects arguments with flags allowed on either side of them.
func positionals(env *cliEnv, args []string) ([]string, bool) {
	var pos []string
	for len(args) > 0 {
		if err := env.fs.Parse(args); err != nil {
			return nil, false
		}
		args = env.fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, true
}

func flagVal(env *cliEnv, name string) string {
	if f := env.fs.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

func runNodes(ctx context.Context, env *cliEnv, args []string) int {
	pos, ok := positionals(env, args)
	if !ok {
		return exitUsage
	}
	if len(pos) > 0 && (pos[0] == "show" || pos[0] == "claim") {
		if len(pos) != 3 {
			fmt.Fprintln(env.errOut, nodesUsage)
			return exitUsage
		}
		if pos[0] == "claim" {
			return runNodesClaim(ctx, env, pos[1], pos[2])
		}
		return runNodesShow(ctx, env, pos[1], pos[2])
	}
	if len(pos) != 1 {
		fmt.Fprintln(env.errOut, nodesUsage)
		return exitUsage
	}
	status, typ := flagVal(env, "status"), flagVal(env, "type")
	nodes, err := env.client.nodesFull(ctx, pos[0])
	if err != nil {
		return reportError(env.errOut, err)
	}
	var shown []apiNodeFull
	for _, n := range nodes {
		if (status != "" && n.Status != status) || (typ != "" && n.Type != typ) {
			continue
		}
		shown = append(shown, n)
	}
	if env.limit > 0 && len(shown) > env.limit {
		shown = shown[:env.limit]
	}
	if env.json {
		raws := make([]json.RawMessage, len(shown))
		for i, n := range shown {
			raws[i] = n.Raw
		}
		if err := renderJSON(env.out, raws); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	rows := make([][]string, len(shown))
	for i, n := range shown {
		tier := ""
		if t, ok := nodeData(n.Data)["tier"].(string); ok {
			tier = t
		}
		rows[i] = []string{n.Key, n.Type, n.Status, n.WorkState, nodeDash(tier), nodeAccess(n), n.Title}
	}
	env.table([]string{"KEY", "TYPE", "STATUS", "WORK_STATE", "TIER", "ACCESS", "TITLE"}, rows)
	return exitOK
}

// resolveNode maps a key or id to a node id by listing the graph's nodes.
func resolveNode(ctx context.Context, env *cliEnv, graphID, want string) (string, int) {
	nodes, err := env.client.nodesFull(ctx, graphID)
	if err != nil {
		return "", reportError(env.errOut, err)
	}
	for _, n := range nodes {
		if n.Key == want || n.ID == want {
			return n.ID, exitOK
		}
	}
	fmt.Fprintf(env.errOut, "graphwatch: no node %q in graph %s\n", want, graphID)
	return "", exitRuntime
}

func runNodesShow(ctx context.Context, env *cliEnv, graphID, want string) int {
	id, code := resolveNode(ctx, env, graphID, want)
	if code != exitOK {
		return code
	}
	n, err := env.client.nodeFull(ctx, graphID, id)
	if err != nil {
		return reportError(env.errOut, err)
	}
	runs, err := env.client.nodeRunList(ctx, graphID, id)
	if err != nil {
		return reportError(env.errOut, err)
	}
	if env.json {
		rs := make([]apiNodeRunRow, len(runs))
		copy(rs, runs)
		if err := renderJSON(env.out, map[string]any{"node": n.Raw, "runs": rs}); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	w := env.out
	fmt.Fprintf(w, "key:         %s\nid:          %s\ntype:        %s\ntitle:       %s\n", clean(n.Key), clean(n.ID), clean(n.Type), clean(n.Title))
	fmt.Fprintf(w, "status:      %s\nwork_state:  %s\n", n.Status, nodeDash(n.WorkState))
	if n.Reclaimable {
		fmt.Fprintln(w, "reclaimable: true")
	}
	if n.OpensAt != nil {
		fmt.Fprintf(w, "opens_at:    %s\n", n.OpensAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	fmt.Fprintf(w, "assignee:    %s\n", nodeDash(n.Assignee.display()))
	fmt.Fprintf(w, "assignment:  %s\n", clean(assignmentText(n)))
	// private is printed only when true, the same way reclaimable is.
	if n.Private {
		fmt.Fprintln(w, "private:     true")
	}
	if n.Description != "" {
		fmt.Fprintf(w, "\ndescription:\n%s\n", clean(n.Description))
	}
	if acc, ok := nodeData(n.Data)["acceptance"].([]any); ok && len(acc) > 0 {
		fmt.Fprintln(w, "\nacceptance:")
		for _, a := range acc {
			fmt.Fprintf(w, "  %s\n", clean(fmt.Sprint(a)))
		}
	}
	fmt.Fprintf(w, "\nruns: %d\n", len(runs))
	rows := make([][]string, len(runs))
	for i, r := range runs {
		runner, _ := nodeData(r.Data)["runner"].(string)
		rows[i] = []string{fmt.Sprint(r.Attempt), r.Status, nodeDash(strings.TrimSpace(runner)), r.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
	}
	env.table([]string{"ATTEMPT", "STATUS", "RUNNER", "CREATED"}, rows)
	return exitOK
}

// assignmentText is the value of the assignment: line. advisory is the
// server's default, including a node read that predates the field. required
// names who may run it; the client does not decide that itself.
func assignmentText(n apiNodeFull) string {
	mode := n.AssignmentMode
	if mode == "" {
		mode = "advisory"
	}
	if mode != "required" {
		return mode
	}
	name, group := n.Assignee.account(), n.Assignee.group()
	switch {
	case group != "" && name == "":
		return "required (only the " + group + " group can run this)"
	case name != "" && group != "":
		return "required (only " + name + " or the " + group + " group can run this)"
	case name != "":
		return "required (only " + name + " can run this)"
	default:
		return "required (only someone else can run this)"
	}
}

// nodeAccess is the ACCESS column: the server's claim_block and private flag,
// not a membership check. Plain ASCII. Colour stays with the table header,
// which is the only colour a table already uses on a terminal.
func nodeAccess(n apiNodeFull) string {
	reserved := n.ClaimBlock != nil && *n.ClaimBlock == "node_reserved"
	switch {
	case reserved && n.Private:
		return "reserved,local"
	case reserved:
		return "reserved"
	case n.Private:
		return "local"
	default:
		return "-"
	}
}

// reservedClaimMessage is the stderr line for a 409 node_reserved. who is the
// assignee when the node read carried one; otherwise "someone else".
func reservedClaimMessage(key, who string) string {
	who = strings.TrimSpace(who)
	if who == "" {
		who = "someone else"
	}
	return fmt.Sprintf("graphwatch: node %s is reserved for %s; only they can run it (node_reserved)", clean(key), clean(who))
}

func nodeReserved(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) || ae.status != 409 {
		return false
	}
	return ae.code == "node_reserved" || ae.detail == "node_reserved"
}

func runNodesClaim(ctx context.Context, env *cliEnv, graphID, want string) int {
	nodes, err := env.client.nodesFull(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	node := findNode(nodes, want)
	if node == nil {
		fmt.Fprintf(env.errOut, "graphwatch: no node %q in graph %s\n", want, graphID)
		return exitRuntime
	}
	raw, err := env.client.claimNode(ctx, graphID, node.ID)
	if err != nil {
		if nodeReserved(err) {
			fmt.Fprintln(env.errOut, reservedClaimMessage(node.Key, node.Assignee.who()))
			return exitRuntime
		}
		return reportError(env.errOut, err)
	}
	if env.json {
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		if err := renderJSON(env.out, raw); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	var card struct {
		Key   string `json:"key"`
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal(raw, &card)
	key := card.Key
	if key == "" {
		key = node.Key
	}
	fmt.Fprintf(env.out, "claimed %s\n", clean(key))
	if card.RunID != "" {
		fmt.Fprintf(env.out, "run %s\n", clean(card.RunID))
	}
	return exitOK
}
