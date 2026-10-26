package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
)

func init() {
	register("access", "who can reach a graph: access <graph> [--full-ids]", runAccess)
	// Declared here so `help access` lists it; takeFullIDs still accepts it
	// after the graph id, where the shared flag set has stopped parsing.
	commandFlags["access"] = func(fs *flag.FlagSet) {
		fs.Bool("full-ids", false, "print whole ids, not the first 8 characters")
	}
}

// notFound turns the API's deliberate 404 for an invisible graph into one
// message; it does not say whether the graph exists.
func notFound(env *cliEnv, err error) (int, bool) {
	var ae *apiError
	if errors.As(err, &ae) && ae.status == 404 {
		fmt.Fprintln(env.errOut, "graphwatch: graph not found")
		return exitRuntime, true
	}
	return 0, false
}

// takeFullIDs pulls --full-ids out of args. The global flag set stops at the
// first positional, so the flag is accepted after the graph id.
func takeFullIDs(args []string) (rest []string, full bool) {
	for _, a := range args {
		if a == "--full-ids" || a == "-full-ids" {
			full = true
		} else {
			rest = append(rest, a)
		}
	}
	return rest, full
}

func shortID(id string, full bool) string {
	if full || len(id) <= 8 {
		return id
	}
	return id[:8]
}

func viaLabel(v []apiAccessSource) string {
	var parts []string
	for _, s := range v {
		switch s.Kind {
		case "creator":
			parts = append(parts, "owner")
		case "member", "share":
			parts = append(parts, "member")
		case "group":
			parts = append(parts, "group:"+s.GroupSlug)
		default:
			parts = append(parts, s.Kind)
		}
	}
	return strings.Join(parts, ",")
}

func runAccess(ctx context.Context, env *cliEnv, args []string) int {
	args, full := takeFullIDs(args)
	full = full || flagSet(env, "full-ids")
	if len(args) != 1 {
		fmt.Fprintln(env.errOut, "usage: graphwatch access <graph> [--full-ids]")
		return exitUsage
	}
	acc, raw, err := env.client.access(ctx, args[0])
	if err != nil {
		if code, ok := notFound(env, err); ok {
			return code
		}
		return reportError(env.errOut, err)
	}
	if env.json {
		if err := renderJSON(env.out, raw); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	var rows [][]string
	for _, a := range acc.Accounts {
		name := a.Username
		if name == "" {
			name = a.Email
		}
		who := shortID(a.AccountID, full)
		if full {
			if name != "" {
				who = name + " " + a.AccountID
			}
		} else if name != "" {
			who = name
		}
		rows = append(rows, []string{who, a.Role, viaLabel(a.Via)})
	}
	if env.limit > 0 && len(rows) > env.limit {
		rows = rows[:env.limit]
	}
	env.table([]string{"ACCOUNT", "ROLE", "VIA"}, rows)
	if acc.GroupsUnresolved {
		fmt.Fprintln(env.errOut, "graphwatch: some group members could not be listed; the table may be incomplete")
	}
	return exitOK
}
