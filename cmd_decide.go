package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
)

func init() {
	register("decide", "record an arbitrator's decision on a gate node (a write): decide <graph> <node key|id> <decision> [--note text] [--follow-up key]...", runDecide)
	commandFlags["decide"] = func(fs *flag.FlagSet) {
		fs.String("note", "", "context for the decision, at most 4000 characters")
		fs.Var(new(repeated), "follow-up", "node key to work next, recorded with the decision; repeat for several")
	}
}

const decideUsage = "usage: graphwatch decide <graph> <node key|id> <decision> [--note text] [--follow-up key]..."

// repeated is a string flag that may be given more than once.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func runDecide(ctx context.Context, env *cliEnv, args []string) int {
	fs := flag.NewFlagSet("decide", flag.ContinueOnError)
	fs.SetOutput(env.errOut)
	note := fs.String("note", "", "")
	var follow repeated
	fs.Var(&follow, "follow-up", "")
	// Flags may follow the positional arguments, and the shared set may already
	// have parsed some that preceded them.
	if f := env.fs.Lookup("note"); f != nil {
		*note = f.Value.String()
	}
	if f := env.fs.Lookup("follow-up"); f != nil {
		if v, ok := f.Value.(*repeated); ok {
			follow = append(follow, *v...)
		}
	}
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
		fmt.Fprintln(env.errOut, decideUsage)
		return exitUsage
	}
	graphID, ref, decision := pos[0], pos[1], pos[2]

	nodes, err := env.client.nodesFull(ctx, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	node := findNode(nodes, ref)
	if node == nil {
		fmt.Fprintf(env.errOut, "graphwatch: no node %q in graph %s\n", ref, graphID)
		return exitRuntime
	}
	resp, err := env.client.decideGate(ctx, graphID, node.ID, decideRequest{Decision: decision, Note: *note, FollowUps: follow})
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			switch {
			case ae.status == 403:
				fmt.Fprintln(env.errOut, "you are not an arbitrator on this graph")
				return exitRuntime
			case ae.status == 409 && ae.code == "not_a_gate":
				fmt.Fprintf(env.errOut, "%s is not a gate\n", node.Key)
				return exitRuntime
			}
		}
		return reportError(env.errOut, err)
	}
	status := "unknown"
	if after, err := env.client.nodesFull(ctx, graphID); err == nil {
		if n := findNode(after, node.ID); n != nil {
			status = n.Status
		}
	}
	if env.json {
		if err := renderJSON(env.out, map[string]any{"node": node.Key, "status": status, "frontier": resp.Frontier}); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	fmt.Fprintf(env.out, "%s decided %q: status %s\n", clean(node.Key), decision, clean(status))
	fmt.Fprintf(env.out, "released %d:\n", len(resp.Frontier))
	for _, f := range resp.Frontier {
		fmt.Fprintf(env.out, "  %s  %s\n", clean(f.Key), clean(f.Title))
	}
	return exitOK
}

// findNode matches a key or an id exactly.
func findNode(nodes []apiNodeFull, ref string) *apiNodeFull {
	for i := range nodes {
		if nodes[i].ID == ref || nodes[i].Key == ref {
			return &nodes[i]
		}
	}
	return nil
}
