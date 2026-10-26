package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"
)

func init() {
	register("graphs", "list the graphs you can see, or show one with its roll-up: graphs [list] [--archived] [--full-ids] | graphs show <id|slug>", runGraphs)
	commandFlags["graphs"] = func(fs *flag.FlagSet) {
		fs.Bool("archived", false, "include archived graphs")
		fs.Bool("full-ids", false, "print whole ids, not the first 8 characters")
	}
}

const graphsUsage = "usage: graphwatch graphs [list] [--archived] [--full-ids]\n       graphwatch graphs show <id|slug>"

func runGraphs(ctx context.Context, env *cliEnv, args []string) int {
	pos, ok := positionals(env, args)
	if !ok {
		return exitUsage
	}
	if len(pos) > 0 && pos[0] == "show" {
		if len(pos) != 2 {
			fmt.Fprintln(env.errOut, graphsUsage)
			return exitUsage
		}
		return runGraphsShow(ctx, env, pos[1])
	}
	if len(pos) > 1 || (len(pos) == 1 && pos[0] != "list") {
		fmt.Fprintln(env.errOut, graphsUsage)
		return exitUsage
	}
	graphs, err := env.client.graphsList(ctx)
	if err != nil {
		return reportError(env.errOut, err)
	}
	withArchived := flagVal(env, "archived") == "true"
	var shown []apiGraphRow
	for _, g := range graphs {
		if g.ArchivedAt != "" && !withArchived {
			continue
		}
		shown = append(shown, g)
	}
	if env.limit > 0 && len(shown) > env.limit {
		shown = shown[:env.limit]
	}
	if env.json || flagVal(env, "json") == "true" {
		raws := make([]json.RawMessage, len(shown))
		for i, g := range shown {
			raws[i] = g.Raw
		}
		if err := renderJSON(env.out, raws); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	full := flagVal(env, "full-ids") == "true"
	hasState := false
	for _, g := range shown {
		hasState = hasState || g.State != ""
	}
	headers := []string{"ID", "SLUG", "NAME", "MODE", "LIFECYCLE"}
	if hasState {
		headers = append(headers, "STATE")
	}
	rows := make([][]string, len(shown))
	for i, g := range shown {
		id := g.ID
		if !full && len(id) > 8 {
			id = id[:8]
		}
		rows[i] = []string{id, nodeDash(g.Slug), g.Name, g.Mode, nodeDash(g.Lifecycle)}
		if hasState {
			rows[i] = append(rows[i], nodeDash(g.State))
		}
	}
	env.table(headers, rows)
	return exitOK
}

// resolveGraph maps an id or slug to a graph id by listing. Anything that is
// not an exact id or slug is exit 2 naming the candidates.
func resolveGraph(ctx context.Context, env *cliEnv, want string) (string, int) {
	graphs, err := env.client.graphsList(ctx)
	if err != nil {
		return "", reportError(env.errOut, err)
	}
	var ids, hints []string
	for _, g := range graphs {
		if g.ID == want {
			return g.ID, exitOK
		}
		if g.Slug == want {
			ids = append(ids, g.ID)
		}
		if g.Slug != "" && (strings.HasPrefix(g.Slug, want) || strings.HasPrefix(g.ID, want)) {
			hints = append(hints, g.Slug+" ("+g.ID+")")
		}
	}
	if len(ids) == 1 {
		return ids[0], exitOK
	}
	if len(ids) > 1 {
		fmt.Fprintf(env.errOut, "graphwatch: slug %q is ambiguous: %s\n", want, strings.Join(ids, ", "))
		return "", exitUsage
	}
	sort.Strings(hints)
	msg := fmt.Sprintf("graphwatch: no graph with id or slug %q", want)
	if len(hints) > 0 {
		msg += "; candidates: " + strings.Join(hints, ", ")
	}
	fmt.Fprintln(env.errOut, msg)
	return "", exitUsage
}

func runGraphsShow(ctx context.Context, env *cliEnv, want string) int {
	id, code := resolveGraph(ctx, env, want)
	if code != exitOK {
		return code
	}
	var parts [3]json.RawMessage
	for i, suffix := range []string{"", "/summary", "/stats"} {
		d, err := env.client.graphData(ctx, "/graphs/"+id+suffix)
		if err != nil {
			return reportError(env.errOut, err)
		}
		parts[i] = d
	}
	if env.json || flagVal(env, "json") == "true" {
		if err := renderJSON(env.out, map[string]json.RawMessage{"graph": parts[0], "summary": parts[1], "stats": parts[2]}); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	var g, sum, stats map[string]any
	_ = json.Unmarshal(parts[0], &g)
	_ = json.Unmarshal(parts[1], &sum)
	_ = json.Unmarshal(parts[2], &stats)

	w := env.out
	fmt.Fprintln(w, "== identity")
	kv(env, [][]string{
		{"id", str(g["id"])}, {"slug", str(g["slug"])}, {"name", str(g["name"])},
		{"mode", str(g["mode"])}, {"lifecycle", str(g["lifecycle"])}, {"epoch", str(g["epoch"])},
		{"role", str(g["role"])}, {"nodes", str(g["node_count"])}, {"edges", str(g["edge_count"])},
	})
	fmt.Fprintln(w, "\n== summary")
	kv(env, [][]string{{"state", str(sum["state"])}, {"complete", str(sum["complete"])}, {"idle", str(sum["idle"])}})
	fmt.Fprintln(w, "\nby status:")
	kv(env, sortedCounts(sum["by_status"]))
	fmt.Fprintln(w, "\nby work_state:")
	kv(env, sortedCounts(sum["by_work_state"]))
	fmt.Fprintln(w, "\nworkers and demand:")
	rows := sortedCounts(sum["workers"])
	if d, ok := sum["demand"].(map[string]any); ok {
		for _, k := range []string{"frontier_size", "workers_wanted", "judges_wanted", "reclaimable"} {
			if v, ok := d[k]; ok {
				rows = append(rows, []string{"demand." + k, str(v)})
			}
		}
	}
	kv(env, rows)
	fmt.Fprintln(w, "\n== stats")
	if t, ok := stats["totals"].(map[string]any); ok {
		kv(env, sortedCounts(t))
	}
	return exitOK
}

func kv(env *cliEnv, rows [][]string) {
	if len(rows) == 0 {
		fmt.Fprintln(env.out, "  -")
		return
	}
	width := 0
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	for _, r := range rows {
		fmt.Fprintf(env.out, "  %-*s  %s\n", width, r[0], r[1])
	}
}

// sortedCounts flattens a JSON object's scalar members, keys sorted; the
// server's names are printed verbatim.
func sortedCounts(v any) [][]string {
	m, _ := v.(map[string]any)
	keys := make([]string, 0, len(m))
	for k, x := range m {
		switch x.(type) {
		case map[string]any, []any:
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][]string, len(keys))
	for i, k := range keys {
		rows[i] = []string{k, str(m[k])}
	}
	return rows
}

func str(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case string:
		return nodeDash(x)
	case float64:
		return fmt.Sprintf("%g", x)
	}
	return fmt.Sprint(v)
}
