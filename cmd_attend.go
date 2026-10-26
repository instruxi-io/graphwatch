package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

func init() {
	register("attend", "follow a graph and take your answers inline (writes only when you answer): attend [--poll secs] <graph>", runAttend)
	commandFlags["attend"] = func(fs *flag.FlagSet) {
		fs.Int("poll", 15, "seconds between inbox re-reads when the stream is quiet or unavailable")
	}
}

const attendUsage = "usage: graphwatch attend [--poll secs] <graph>"

const attendHelp = `<n> approve|reject|override [note...]   answer review item n (override needs a note)
<n> decide <decision> [note...]         decide gate n
list                                    show what needs you
help                                    this text
quit                                    leave (end of input also leaves)`

// attendIn is where answers are read from. It is a variable so a test can
// script it; input is line based on purpose, which works on every platform
// with no terminal mode.
var attendIn io.Reader = os.Stdin

func runAttend(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errOut, attendUsage)
		return exitUsage
	}
	graphID := args[0]
	poll, _ := strconv.Atoi(env.fs.Lookup("poll").Value.String())
	if poll <= 0 {
		fmt.Fprintln(env.errOut, "graphwatch: --poll must be a positive number of seconds")
		return exitUsage
	}
	// Numbers are given on first sight and never reused, so "2" still means
	// the same item after another one is answered.
	nums := map[string]int{}
	next := 0
	var current []inboxEntry
	numOf := func(e inboxEntry) int { return nums[e.Type+":"+e.ID] }

	// Every item not yet numbered is announced, whichever read finds it: one
	// that arrives while you are answering another must not wait for `list`.
	refresh := func() bool {
		entries, err := collectInbox(ctx, env.client, graphID)
		if err != nil {
			reportError(env.errOut, err)
			return false
		}
		bell := false
		for _, e := range entries {
			k := e.Type + ":" + e.ID
			if _, seen := nums[k]; !seen {
				next++
				nums[k] = next
				printAttendItem(env.out, next, e)
				bell = true
			}
		}
		changed := len(entries) != len(current)
		current = entries
		if bell {
			fmt.Fprint(env.out, "\a")
		}
		if bell || changed {
			fmt.Fprintf(env.out, "%d need you\n", len(current))
		}
		return true
	}

	// The first read prints everything already waiting.
	entries, err := collectInbox(ctx, env.client, graphID)
	if err != nil {
		return reportError(env.errOut, err)
	}
	if len(entries) == 0 {
		fmt.Fprintln(env.out, "nothing needs you")
	}
	for _, e := range entries {
		next++
		nums[e.Type+":"+e.ID] = next
		printAttendItem(env.out, next, e)
	}
	current = entries
	if len(entries) > 0 {
		fmt.Fprintf(env.out, "\a%d need you\n", len(entries))
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Every stream event means re-read; the stream is only a clock. When it is
	// refused or drops, the ticker below still covers the same ground.
	wake := make(chan struct{}, 1)
	go env.client.streamFrom(ctx, graphID, "", func(sseEvent) {
		select {
		case wake <- struct{}{}:
		default:
		}
	}, func(string) {})

	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(attendIn)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	tick := time.NewTicker(time.Duration(poll) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return exitOK
		case <-wake:
			refresh()
		case <-tick.C:
			refresh()
		case line, ok := <-lines:
			if !ok {
				return exitOK
			}
			if !attendLine(ctx, env, graphID, line, current, numOf) {
				return exitOK
			}
			refresh()
		}
	}
}

func printAttendItem(w io.Writer, n int, e inboxEntry) {
	if e.Type == "gate" {
		line := fmt.Sprintf("  %d  gate  %s  %s", n, clean(e.Node), clean(e.Title))
		if len(e.Options) > 0 {
			line += " [" + clean(strings.Join(e.Options, " | ")) + "]"
		}
		fmt.Fprintln(w, line)
		return
	}
	fmt.Fprintf(w, "  %d  %s  %s  %s  %s\n", n, inboxFields(e)[0], clean(e.Node), e.Age, clean(e.Detail))
}

// attendLine runs one line of input and reports false when the person quit.
func attendLine(ctx context.Context, env *cliEnv, graphID, line string, current []inboxEntry, numOf func(inboxEntry) int) bool {
	f := strings.Fields(line)
	if len(f) == 0 {
		return true
	}
	switch f[0] {
	case "quit", "exit", "q":
		return false
	case "help", "?":
		fmt.Fprintln(env.out, attendHelp)
		return true
	case "list", "ls":
		if len(current) == 0 {
			fmt.Fprintln(env.out, "nothing needs you")
		}
		for _, e := range current {
			printAttendItem(env.out, numOf(e), e)
		}
		return true
	}
	n, err := strconv.Atoi(f[0])
	if err != nil || len(f) < 2 {
		fmt.Fprintln(env.errOut, "not understood; type help")
		return true
	}
	var item *inboxEntry
	for i := range current {
		if numOf(current[i]) == n {
			item = &current[i]
		}
	}
	if item == nil {
		fmt.Fprintf(env.errOut, "no item %d\n", n)
		return true
	}
	// Answers go through runResolve and runDecide, the code behind the
	// resolve and decide commands, so refusals read the same everywhere.
	switch {
	case item.Type == "review" && (f[1] == "approve" || f[1] == "reject" || f[1] == "override"):
		runResolve(ctx, noteEnv(env, strings.Join(f[2:], " ")), []string{graphID, item.ID, f[1]})
	case item.Type == "gate" && f[1] == "decide":
		if len(f) < 3 {
			fmt.Fprintln(env.errOut, "decide needs a decision")
			return true
		}
		if len(item.Options) > 0 && !contains(item.Options, f[2]) {
			fmt.Fprintf(env.errOut, "%q is not one of the options: %s\n", f[2], clean(strings.Join(item.Options, " | ")))
			return true
		}
		runDecide(ctx, noteEnv(env, strings.Join(f[3:], " ")), []string{graphID, item.ID, f[2]})
	case item.Type == "review":
		fmt.Fprintf(env.errOut, "item %d is a review item: %d approve|reject|override [note...]\n", n, n)
	default:
		fmt.Fprintf(env.errOut, "item %d is a gate: %d decide <decision> [note...]\n", n, n)
	}
	return true
}

// noteEnv copies env with a flag set that carries the note, which is where
// runResolve and runDecide read it from.
func noteEnv(env *cliEnv, note string) *cliEnv {
	fs := flag.NewFlagSet("attend", flag.ContinueOnError)
	fs.String("note", note, "")
	cp := *env
	cp.fs = fs
	return &cp
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
