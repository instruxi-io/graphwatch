package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// recallNow is the clock --since measures against; tests replace it.
var recallNow = time.Now

// maxRecallLimit is the largest page the recall endpoint accepts.
const maxRecallLimit = 50

// maxRecallScan bounds how many hits --since or --sort time will read.
const maxRecallScan = 500

// decodeHits reads a recall payload: a list of hits, or an object that carries
// them under "hits".
func decodeHits(d json.RawMessage) ([]recallHit, error) {
	var hits []recallHit
	if err := json.Unmarshal(d, &hits); err == nil {
		return hits, nil
	}
	var wrapped struct {
		Hits []recallHit `json:"hits"`
	}
	if err := json.Unmarshal(d, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Hits, nil
}

// parseSince reads a Go duration, or a whole number of days written as "3d".
func parseSince(v string) (time.Duration, bool) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return 0, false
		}
		return time.Duration(days) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, false
	}
	return d, true
}

func init() {
	register("recall", "search a graph's memory: recall <graph> [--q text] [--node key|id] [--file path] [--kind k] [--since 30m|2h|3d] [--sort rank|time]", runRecall)
	commandFlags["recall"] = func(fs *flag.FlagSet) {
		fs.String("q", "", "full-text query over the observations' bodies")
		fs.String("node", "", "only observations about this node (key or id)")
		fs.String("file", "", "only observations that mention this file path")
		fs.String("kind", "", "only observations of this kind")
		fs.String("since", "", "only observations created within this long ago (30m, 2h, 3d)")
		fs.String("sort", "rank", "order of the hits: rank (the server's order) or time (newest first)")
	}
	register("context", "show a graph's context pack: its hash, size, build time and whether recall is on: context <graph>", runContext)
}

// recallHit is one observation as the recall endpoint returns it. Every field is
// optional on the wire; a missing one prints as a dash.
type recallHit struct {
	Kind      string `json:"kind"`
	NodeKey   string `json:"node_key"`
	NodeID    string `json:"node_id"`
	CreatedAt string `json:"created_at"`
	RunID     string `json:"run_id"`
	Body      string `json:"body"`
}

// graphGet reads path under a graph and maps a 404 to the one-line "no graph"
// answer. The graph is taken as given: a recall is one read, so there is no
// listing to resolve a slug against.
func graphGet(ctx context.Context, env *cliEnv, graph, suffix string, q url.Values) (json.RawMessage, int) {
	path := "/graphs/" + url.PathEscape(graph) + suffix
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	d, err := env.client.graphData(ctx, path)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.status == http.StatusNotFound {
			fmt.Fprintf(env.errOut, "graphwatch: no graph %s\n", graph)
			return nil, exitRuntime
		}
		return nil, reportError(env.errOut, err)
	}
	return d, exitOK
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return dash(s)
}

func runRecall(ctx context.Context, env *cliEnv, args []string) int {
	pos, ok := positionals(env, args)
	if !ok || len(pos) != 1 {
		fmt.Fprintln(env.errOut, "usage: graphwatch recall <graph> [--q text] [--node key|id] [--file path] [--kind k] [--limit n]")
		return exitUsage
	}
	var window time.Duration
	if v := flagVal(env, "since"); v != "" {
		var ok bool
		if window, ok = parseSince(v); !ok {
			fmt.Fprintf(env.errOut, "--since: bad duration %s\n", v)
			return exitUsage
		}
		if window <= 0 {
			fmt.Fprintf(env.errOut, "--since: must be positive, got %s\n", v)
			return exitUsage
		}
	}
	order := flagVal(env, "sort")
	if order != "" && order != "rank" && order != "time" {
		fmt.Fprintln(env.errOut, "--sort must be rank or time")
		return exitUsage
	}
	// The server caps a page at 50; refuse here rather than send a request it rejects.
	if env.limit > maxRecallLimit {
		fmt.Fprintf(env.errOut, "--limit: at most %d\n", maxRecallLimit)
		return exitUsage
	}
	q := url.Values{}
	for _, name := range []string{"q", "node", "file", "kind"} {
		if v := flagVal(env, name); v != "" {
			q.Set(name, v)
		}
	}
	asJSON := env.json || flagVal(env, "json") == "true"
	// --since and --sort time act on the whole result set, so they read pages
	// (50 a page, at most maxRecallScan hits) and cut to --limit afterwards.
	// Otherwise one page of --limit hits is enough.
	scan := !asJSON && (window > 0 || order == "time")
	var hits []recallHit
	if scan {
		q.Set("limit", strconv.Itoa(maxRecallLimit))
		for off := 0; off < maxRecallScan; off += maxRecallLimit {
			q.Set("offset", strconv.Itoa(off))
			d, code := graphGet(ctx, env, pos[0], "/recall", q)
			if code != exitOK {
				return code
			}
			page, err := decodeHits(d)
			if err != nil {
				return reportError(env.errOut, err)
			}
			hits = append(hits, page...)
			if len(page) < maxRecallLimit {
				break
			}
			if off+maxRecallLimit >= maxRecallScan {
				fmt.Fprintf(env.errOut, "graphwatch: read the first %d hits only; narrow the search with --q, --node or --kind\n", maxRecallScan)
			}
		}
	} else {
		if env.limit > 0 {
			q.Set("limit", strconv.Itoa(env.limit))
		}
		d, code := graphGet(ctx, env, pos[0], "/recall", q)
		if code != exitOK {
			return code
		}
		if asJSON {
			fmt.Fprintln(env.out, string(d))
			return exitOK
		}
		var err error
		if hits, err = decodeHits(d); err != nil {
			return reportError(env.errOut, err)
		}
	}
	if window > 0 {
		cutoff := recallNow().Add(-window)
		kept := hits[:0]
		for _, h := range hits {
			// A hit with no readable timestamp cannot be shown to be recent.
			if t, err := time.Parse(time.RFC3339, h.CreatedAt); err == nil && !t.Before(cutoff) {
				kept = append(kept, h)
			}
		}
		hits = kept
	}
	if order == "time" {
		// Stable, so hits with equal or unreadable timestamps keep the server's order.
		sort.SliceStable(hits, func(i, j int) bool {
			ti, ei := time.Parse(time.RFC3339, hits[i].CreatedAt)
			tj, ej := time.Parse(time.RFC3339, hits[j].CreatedAt)
			if ei != nil || ej != nil {
				return ei == nil && ej != nil
			}
			return ti.After(tj)
		})
	}
	// The server honours limit; this also bounds a server that does not.
	if env.limit > 0 && len(hits) > env.limit {
		hits = hits[:env.limit]
	}
	if len(hits) == 0 {
		fmt.Fprintln(env.out, "no findings")
		return exitOK
	}
	rows := make([][]string, len(hits))
	for i, h := range hits {
		node := h.NodeKey
		if node == "" {
			node = h.NodeID
		}
		rows[i] = []string{dash(h.Kind), dash(node), dash(h.CreatedAt), dash(h.RunID), firstLine(h.Body)}
	}
	env.table([]string{"KIND", "NODE", "CREATED", "RUN", "BODY"}, rows)
	return exitOK
}

// contextInfo is the pack's row on the graph, as GET /graphs/{id}/context returns it.
type contextInfo struct {
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	BuiltAt        string `json:"built_at"`
	RecallEnabled  bool   `json:"recall_enabled"`
	ContextEnabled *bool  `json:"context_enabled"`
}

func runContext(ctx context.Context, env *cliEnv, args []string) int {
	pos, ok := positionals(env, args)
	if !ok || len(pos) != 1 {
		fmt.Fprintln(env.errOut, "usage: graphwatch context <graph>")
		return exitUsage
	}
	d, code := graphGet(ctx, env, pos[0], "/context", nil)
	if code != exitOK {
		return code
	}
	if env.json || flagVal(env, "json") == "true" {
		fmt.Fprintln(env.out, string(d))
		return exitOK
	}
	var c contextInfo
	if err := json.Unmarshal(d, &c); err != nil {
		return reportError(env.errOut, err)
	}
	if c.SHA256 == "" {
		fmt.Fprintln(env.out, "no context pack built yet")
	} else {
		env.table([]string{"FIELD", "VALUE"}, [][]string{
			{"sha256", c.SHA256},
			{"size", fmt.Sprintf("%d bytes", c.Size)},
			{"built at", dash(c.BuiltAt)},
			{"recall enabled", strconv.FormatBool(c.RecallEnabled)},
		})
	}
	return exitOK
}
