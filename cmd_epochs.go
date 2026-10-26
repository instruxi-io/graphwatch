package main

import (
	"context"
	"fmt"
	"strconv"
)

func init() {
	register("epochs", "replay history: epochs <graph> | epochs show <graph> <epoch>", runEpochs)
}

var epochHeaders = []string{"EPOCH", "STARTED", "ENDED", "NODES", "DONE", "FAILED", "REASON"}

func epochRow(e apiEpoch) []string {
	n := strconv.Itoa(e.Epoch)
	ended := "-"
	if e.EndedAt != nil {
		ended = *e.EndedAt
	}
	if e.Current {
		n += " *"
		ended = "(current)"
	}
	return []string{n, e.StartedAt, ended, strconv.Itoa(e.Stats.Nodes),
		strconv.Itoa(e.Stats.Done), strconv.Itoa(e.Stats.Failed), e.StartReason}
}

func runEpochs(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) > 0 && args[0] == "show" {
		return runEpochsShow(ctx, env, args[1:])
	}
	if len(args) != 1 {
		fmt.Fprintln(env.errOut, "usage: graphwatch epochs <graph> | graphwatch epochs show <graph> <epoch>")
		return exitUsage
	}
	list, err := env.client.epochs(ctx, args[0])
	if err != nil {
		if code, ok := notFound(env, err); ok {
			return code
		}
		return reportError(env.errOut, err)
	}
	if env.limit > 0 && len(list) > env.limit {
		list = list[:env.limit]
	}
	if env.json {
		if err := renderJSON(env.out, list); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	rows := make([][]string, 0, len(list))
	for _, e := range list {
		rows = append(rows, epochRow(e))
	}
	env.table(epochHeaders, rows)
	return exitOK
}

func runEpochsShow(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(env.errOut, "usage: graphwatch epochs show <graph> <epoch>")
		return exitUsage
	}
	n, err := strconv.Atoi(args[1])
	if err != nil || n < 0 {
		fmt.Fprintln(env.errOut, "graphwatch: epoch must be a non-negative number")
		return exitUsage
	}
	e, err := env.client.epoch(ctx, args[0], n)
	if err != nil {
		if code, ok := notFound(env, err); ok {
			return code
		}
		return reportError(env.errOut, err)
	}
	if env.json {
		if err := renderJSON(env.out, e); err != nil {
			return reportError(env.errOut, err)
		}
		return exitOK
	}
	env.table(epochHeaders, [][]string{epochRow(e)})
	return exitOK
}
