package main

import (
	"context"
	"flag"
	"fmt"
)

func init() {
	register("edges", "list a graph's dependencies: edges <graph> [--as-flow]", runEdges)
	commandFlags["edges"] = func(fs *flag.FlagSet) {
		fs.Bool("as-flow", false, "print prerequisite -> dependent instead of the stored dependent -> prerequisite")
	}
}

const edgesUsage = "usage: graphwatch edges <graph> [--as-flow]"

func runEdges(ctx context.Context, env *cliEnv, args []string) int {
	pos, ok := positionals(env, args)
	if !ok {
		return exitUsage
	}
	if len(pos) != 1 {
		fmt.Fprintln(env.errOut, edgesUsage)
		return exitUsage
	}
	flow := flagVal(env, "as-flow") == "true"
	nodes, err := env.client.nodes(ctx, pos[0])
	if err != nil {
		return reportError(env.errOut, err)
	}
	edges, err := env.client.edges(ctx, pos[0])
	if err != nil {
		return reportError(env.errOut, err)
	}
	if env.limit > 0 && len(edges) > env.limit {
		edges = edges[:env.limit]
	}
	keys := map[string]string{}
	for _, n := range nodes {
		keys[n.ID] = n.Key
	}
	name := func(id string) string {
		if k := keys[id]; k != "" {
			return k
		}
		return id
	}
	if env.json {
		if err := renderJSON(env.out, edges); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	// A requires edge is stored dependent -> prerequisite. The default output
	// keeps that direction and says so in the header; --as-flow swaps it.
	rows := make([][]string, len(edges))
	head := []string{"FROM_KEY", "", "TO_KEY", "TYPE"}
	if flow {
		head[0], head[2] = "PREREQUISITE", "DEPENDENT"
	}
	for i, e := range edges {
		from, to, typ := name(e.FromNodeID), name(e.ToNodeID), e.Type
		if flow {
			from, to = to, from
			rows[i] = []string{from, "->", to, typ}
		} else {
			rows[i] = []string{from, "depends on", to, typ}
		}
	}
	head[1] = "->"
	if !flow {
		head[1] = "RELATION"
	}
	env.table(head, rows)
	return exitOK
}
