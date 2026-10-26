package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func init() {
	register("query", "query nodes or runs across every graph you can see: query nodes|runs [flags]", runQuery)
	commandFlags["query"] = queryFlags
}

const queryUsage = "usage: graphwatch query nodes [flags]\n       graphwatch query runs [flags]"

// queryParams are the documented parameters of queryNodes and queryRuns, one
// flag each (underscores become dashes). Nothing is validated locally: the
// server's 400 is the answer for an unknown or misused one.
var queryParams = []string{
	"graph_id", "status", "type", "key", "q", "epoch", "assignee", "assignee_group",
	"claimer", "runner", "verdict", "waiting", "link_system", "link_role", "has_output",
	"from", "to", "time_field", "include_archived", "work_state", "sort", "group_by", "agg",
	"tz", "offset", "after", "node_id", "node_key", "node_type", "attempt",
	"verdict_resolution", "min_duration_seconds", "max_duration_seconds", "has_evidence",
}

// queryParamHelp is the one-line meaning of each parameter, so `help query`
// says what a flag filters instead of repeating its name.
var queryParamHelp = map[string]string{
	"graph_id":             "only this graph (id)",
	"status":               "node or run status, e.g. active or done",
	"type":                 "node type",
	"key":                  "node key (exact match)",
	"q":                    "free-text search over node titles and keys",
	"epoch":                "only this epoch number",
	"assignee":             "nodes assigned to this account",
	"assignee_group":       "nodes assigned to this group",
	"claimer":              "runs claimed by this account",
	"runner":               "runs recorded under this runner label",
	"verdict":              "runs with this verification verdict (e.g. verified, rejected)",
	"waiting":              "nodes waiting on this condition (e.g. judges or a human)",
	"link_system":          "nodes linked to this external system",
	"link_role":            "nodes whose link has this role",
	"has_output":           "nodes that declare or hold this output name",
	"from":                 "only rows at or after this time (RFC 3339)",
	"to":                   "only rows before this time (RFC 3339)",
	"time_field":           "which timestamp --from and --to apply to",
	"include_archived":     "true to include archived graphs",
	"work_state":           "the server's work_state, e.g. looking_for_work",
	"sort":                 "sort field, with a leading minus for descending",
	"group_by":             "group rows by this field and print counts instead of rows",
	"agg":                  "aggregate to compute per group, e.g. count or avg:field",
	"tz":                   "time zone for time bucketing, e.g. UTC",
	"offset":               "skip this many rows (grouped results page by offset)",
	"after":                "resume after this page cursor",
	"node_id":              "runs of this node (id)",
	"node_key":             "runs of the node with this key",
	"node_type":            "runs of nodes of this type",
	"attempt":              "runs with this attempt number",
	"verdict_resolution":   "how the verdict was resolved, e.g. judge, vote or human",
	"min_duration_seconds": "runs that took at least this many seconds",
	"max_duration_seconds": "runs that took at most this many seconds",
	"has_evidence":         "true for runs that carry evidence",
}

// whereFlag collects repeated --where path:op:value predicates.
type whereFlag []map[string]any

func (w *whereFlag) String() string { return fmt.Sprint(len(*w)) }

func (w *whereFlag) Set(s string) error {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("want path:op:value, got %q", s)
	}
	p := map[string]any{"path": parts[0], "op": parts[1]}
	if len(parts) == 3 {
		p["value"] = whereValue(parts[1], parts[2])
	}
	*w = append(*w, p)
	return nil
}

// whereValue reads a JSON value when the text is one (3, true, ["a"]) and
// otherwise a string. `in` also takes a bare comma list; prefix and ilike are
// always text.
func whereValue(op, s string) any {
	if op == "prefix" || op == "ilike" {
		return s
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	if op == "in" {
		var out []any
		for _, e := range strings.Split(s, ",") {
			var ev any
			if json.Unmarshal([]byte(e), &ev) != nil {
				ev = e
			}
			out = append(out, ev)
		}
		return out
	}
	return s
}

var queryWhere whereFlag

func queryFlags(fs *flag.FlagSet) {
	queryWhere = nil
	for _, p := range queryParams {
		fs.String(strings.ReplaceAll(p, "_", "-"), "", queryParamHelp[p])
	}
	fs.Var(&queryWhere, "where", "predicate path:op:value, repeatable (ANDed), e.g. data.tier:eq:deep")
	fs.Bool("all", false, "follow the next-page cursor until exhausted, bounded by --limit (default 200)")
}

func runQuery(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) == 0 || (args[0] != "nodes" && args[0] != "runs") {
		fmt.Fprintln(env.errOut, queryUsage)
		return exitUsage
	}
	kind := args[0]
	// Flags may follow the subcommand word; the flag set already holds the
	// ones before it.
	if err := env.fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if env.fs.NArg() > 0 {
		fmt.Fprintln(env.errOut, queryUsage)
		return exitUsage
	}
	params := url.Values{}
	env.fs.Visit(func(f *flag.Flag) {
		for _, p := range queryParams {
			if strings.ReplaceAll(p, "_", "-") == f.Name {
				params.Set(p, f.Value.String())
			}
		}
	})
	if len(queryWhere) > 0 {
		b, _ := json.Marshal([]map[string]any(queryWhere))
		params.Set("where", string(b))
	}
	all := env.fs.Lookup("all").Value.String() == "true"
	// --limit may come after the subcommand word, so read it from the flag set.
	max := env.limit
	fmt.Sscan(env.fs.Lookup("limit").Value.String(), &max)
	if max < 0 {
		fmt.Fprintln(env.errOut, "graphwatch: --limit must be 0 or more")
		return exitUsage
	}
	if max == 0 {
		max = 200
	}
	size := max
	if size > 200 {
		size = 200
	}
	grouped := params.Get("group_by") != ""

	var items []json.RawMessage
	var groups []queryGroup
	var rawPages []json.RawMessage
	for {
		params.Set("limit", fmt.Sprint(size))
		pg, raw, err := env.client.query(ctx, kind, params)
		if err != nil {
			return reportError(env.errOut, err)
		}
		rawPages = append(rawPages, raw)
		items = append(items, pg.Items...)
		groups = append(groups, pg.Groups...)
		// Grouped results page with offset only, so there is no cursor to follow.
		if !all || grouped || pg.Next == "" || len(items) >= max {
			break
		}
		params.Set("after", pg.Next)
		if rem := max - len(items); rem < size {
			size = rem
		}
	}
	if len(items) > max {
		items = items[:max]
	}

	if env.json {
		var out any = rawPages[0]
		if len(rawPages) > 1 {
			out = map[string]any{"items": items}
		}
		if err := renderJSON(env.out, out); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	if grouped {
		printGroups(env, groups)
		return exitOK
	}
	if kind == "nodes" {
		printQueryNodes(env, items)
	} else {
		printQueryRuns(env, items)
	}
	return exitOK
}

func printGroups(env *cliEnv, groups []queryGroup) {
	names := map[string]bool{}
	for _, g := range groups {
		for k := range g.Aggs {
			names[k] = true
		}
	}
	var aggs []string
	for k := range names {
		aggs = append(aggs, k)
	}
	sort.Strings(aggs)
	rows := make([][]string, len(groups))
	for i, g := range groups {
		var ks []string
		for k := range g.Key {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		var kv []string
		for _, k := range ks {
			kv = append(kv, k+"="+scalar(g.Key[k]))
		}
		row := []string{strings.Join(kv, " "), fmt.Sprint(g.Count)}
		for _, a := range aggs {
			row = append(row, scalar(g.Aggs[a]))
		}
		rows[i] = row
	}
	env.table(append([]string{"KEY", "COUNT"}, aggs...), rows)
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case string:
		return dash(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func printQueryNodes(env *cliEnv, items []json.RawMessage) {
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		var n struct {
			GraphID   string `json:"graph_id"`
			Key       string `json:"key"`
			Status    string `json:"status"`
			WorkState string `json:"work_state"`
			Title     string `json:"title"`
		}
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		rows = append(rows, []string{shortID(n.GraphID, false), n.Key, n.Status, dash(n.WorkState), n.Title})
	}
	env.table([]string{"GRAPH", "KEY", "STATUS", "WORK_STATE", "TITLE"}, rows)
}

func printQueryRuns(env *cliEnv, items []json.RawMessage) {
	rows := make([][]string, 0, len(items))
	for _, raw := range items {
		var r struct {
			Attempt int    `json:"attempt"`
			Status  string `json:"status"`
			Runner  string `json:"runner"`
			Client  string `json:"client"`
			Node    struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"node"`
		}
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		runner := r.Runner
		if runner == "" {
			runner = r.Client
		}
		node := r.Node.Key
		if node == "" {
			node = shortID(r.Node.ID, false)
		}
		rows = append(rows, []string{dash(node), fmt.Sprint(r.Attempt), r.Status, dash(runner)})
	}
	env.table([]string{"NODE", "ATTEMPT", "STATUS", "RUNNER"}, rows)
}
