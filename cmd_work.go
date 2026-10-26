package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// minWatchSeconds bounds --watch: each lobby row is one frontier evaluation
// server-side, so polling faster than this only hammers the API.
const minWatchSeconds = 5

func init() {
	register("work", "the work lobby: graphs looking for workers or validation. work [--state s] [--kind work|validation|any] [--graph id] [--watch secs]", runWork)
	commandFlags["work"] = workFlags
	register("recruiting", "what one graph recruits for: recruiting <graph>", runRecruiting)
}

func workFlags(fs *flag.FlagSet) {
	fs.String("state", "", "keep graphs in this roll-up state (e.g. looking_for_work)")
	fs.String("kind", "", "seats to count: work, validation or any (server default)")
	fs.String("tier", "", "node type to count, sent to the server as its type filter")
	fs.String("graph", "", "only this graph (id, slug or name)")
	fs.String("for", "", "me or mine: seats assigned to the caller")
	fs.Int("watch", 0, "re-poll every N seconds (minimum 5)")
}

func watchInterval(secs int) time.Duration {
	if secs < minWatchSeconds {
		secs = minWatchSeconds
	}
	return time.Duration(secs) * time.Second
}

// filterLobby applies the client-side filters; kind, type, skill and group go
// to the server. A graph matches --graph by id, slug or name.
func filterLobby(items []lobbyItem, state, graph string) []lobbyItem {
	var out []lobbyItem
	for _, it := range items {
		if state != "" && it.State != state {
			continue
		}
		if graph != "" && it.GraphID != graph && it.Slug != graph && it.Name != graph {
			continue
		}
		out = append(out, it)
	}
	return out
}

func lobbyHeader(items []lobbyItem) string {
	counts := map[string]int{}
	workers, judges := 0, 0
	for _, it := range items {
		counts[it.State]++
		workers += it.WorkersWant
		judges += it.JudgesWant
	}
	states := make([]string, 0, len(counts))
	for s := range counts {
		states = append(states, s)
	}
	sort.Strings(states)
	parts := make([]string, 0, len(states))
	for _, s := range states {
		parts = append(parts, s+"="+strconv.Itoa(counts[s]))
	}
	return fmt.Sprintf("work: %d graphs, workers_wanted=%d judges_wanted=%d; by work_state: %s",
		len(items), workers, judges, orNone(strings.Join(parts, " ")))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func runWork(ctx context.Context, env *cliEnv, args []string) int {
	fl := func(n string) string { return env.fs.Lookup(n).Value.String() }
	state, kind, tier, graph, forWho := fl("state"), fl("kind"), fl("tier"), fl("graph"), fl("for")
	watch, _ := strconv.Atoi(fl("watch"))
	q := url.Values{}
	for k, v := range map[string]string{"kind": kind, "type": tier, "for": forWho} {
		if v != "" {
			q.Set(k, v)
		}
	}
	for {
		items, err := env.client.openWork(ctx, q)
		if err != nil {
			return reportError(env.errOut, err)
		}
		items = filterLobby(items, state, graph)
		if env.limit > 0 && len(items) > env.limit {
			items = items[:env.limit]
		}
		if env.json {
			if err := renderJSON(env.out, items); err != nil {
				return reportError(env.errOut, err)
			}
		} else {
			fmt.Fprintln(env.out, lobbyHeader(items))
			rows := make([][]string, 0, len(items))
			for _, it := range items {
				name := it.Name
				if name == "" {
					name = it.GraphID
				}
				rows = append(rows, []string{name, it.State,
					fmt.Sprintf("work=%d validation=%d", it.WorkersWant, it.JudgesWant),
					it.OpensNextAt, strconv.Itoa(it.Priority)})
			}
			env.table([]string{"GRAPH", "WORK_STATE", "DEMAND", "OPENS_AT", "PRIORITY"}, rows)
		}
		if watch == 0 {
			return exitOK
		}
		select {
		case <-ctx.Done():
			return exitOK
		case <-time.After(watchInterval(watch)):
		}
	}
}

func runRecruiting(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errOut, "usage: graphwatch recruiting <graph>")
		return exitUsage
	}
	r, err := env.client.recruiting(ctx, args[0])
	if err != nil {
		return reportError(env.errOut, err)
	}
	if env.json {
		if err := renderJSON(env.out, r); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	rc := r.Recruiting
	fmt.Fprintf(env.out, "recruiting: graph=%s open=%t audience=%s\n", r.GraphID, rc.Open, rc.Audience)
	env.table([]string{"FIELD", "VALUE"}, [][]string{
		{"open", strconv.FormatBool(rc.Open)},
		{"audience", rc.Audience},
		{"accounts", orNone(strings.Join(rc.Accounts, ","))},
		{"groups", orNone(strings.Join(rc.Groups, ","))},
		{"max_parallel", strconv.Itoa(rc.MaxParallel)},
		{"priority", strconv.Itoa(rc.Priority)},
		{"groups_unresolved", strconv.FormatBool(r.GroupsUnresolved)},
	})
	return exitOK
}
