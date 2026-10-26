// Command graphwatch draws a graph in the terminal and animates it from its
// activity stream (GET /graphs/{id}/stream): nodes are dots whose shading is
// their state, prerequisites sit above what they unblock so the DAG fans out
// downward, and work shows as heat — pulses flow down an edge into a node when
// it starts and out of it when it finishes, and everything cools when idle.
//
//	GRAPH_AUTH_HELPER='node …/enforcer/<v>/bin/enforcer-headers.mjs' go run . --graph <id>
//	GRAPH_API_KEY=… go run . --graph <id>
//	GRAPH_API_KEY=… go run . --demo          # grow a plan and work it
//	go run . --demo --layout mycelium         # offline: a colony grows, no API needed
//
// GRAPH_AUTH_HELPER (preferred) runs a command that prints the auth headers as
// JSON (the Enforcer plugin's OAuth session from /enforcer:login, refreshed as
// it expires) and wins over GRAPH_API_KEY when both are set.
//
// It reads with the ordinary endpoints (/nodes, /edges) and only uses the
// stream as its clock: every event means "refetch", exactly as a browser would.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"math/rand/v2"
)

func main() {
	if code, ok := dispatch(os.Args[1:], os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	watchMain()
}

// watchFlags holds the bare-flag form's options. They are defined on a flag set
// passed in so a test can list them against the help and the README.
type watchFlags struct {
	base, keyFile, graphID, motion, layout *string
	demoMode, stay, json, noColor          *bool
	size, workers, fps, limit              *int
	failRate                               *float64
	timeout                                *time.Duration
}

func defineWatchFlags(fs *flag.FlagSet) *watchFlags {
	o := &watchFlags{}
	o.base = fs.String("base", "", "api origin (default: GRAPH_BASE_URL, the plugin's saved base_url, then https://api.instruxi.dev)")
	o.keyFile = fs.String("api-key-file", "", "read the API key from this file (first in the credential order)")
	o.graphID = fs.String("graph", "", "graph id to watch")
	o.demoMode = fs.Bool("demo", false, "create a graph, grow it and work it while watching; with --layout mycelium, an offline demo that needs no API")
	o.size = fs.Int("size", 36, "demo: nodes to plan")
	o.workers = fs.Int("workers", 4, "demo: concurrent harnesses")
	o.failRate = fs.Float64("fail", 0.08, "demo: chance a run fails (and is retried)")
	o.stay = fs.Bool("stay", false, "keep watching after the plan completes")
	o.fps = fs.Int("fps", 15, "frames per second (only changed cells are sent)")
	o.motion = fs.String("motion", "events", "events: motion follows graph events and a quiet graph stops drawing; continuous: running nodes pulse and stream particles every frame")
	o.layout = fs.String("layout", "layers", "layers: top-to-bottom layers; mycelium: a radial growth view with roots at the centre")
	fs.BoolVar(&noOverlays, "no-overlays", false, "plain header: no epoch, work_state counts, review holds, recruiting or stream cursor")
	// The table commands' global flags are accepted so a script can pass one set
	// of flags to every form; the watch view says what it does with each.
	o.noColor = fs.Bool("no-color", false, "the watch view needs colour and refuses to draw without it (NO_COLOR is also respected)")
	o.json = fs.Bool("json", false, "table commands only: rejected by the watch view")
	o.limit = fs.Int("limit", 0, "table commands only: rejected by the watch view")
	o.timeout = fs.Duration("timeout", defaultTimeout, "per-request timeout for API calls")
	return o
}

// watchMain is the bare-flag form (--graph, --demo): the default command.
func watchMain() {
	o := defineWatchFlags(flag.CommandLine)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: graphwatch --graph <id> | --demo [flags]   (watch; the default command)")
		fmt.Fprintln(os.Stderr, "       graphwatch <command> [flags]               (graphwatch help lists the commands)")
		fmt.Fprintln(os.Stderr, "\nflags:")
		flag.PrintDefaults()
	}
	flag.Parse()
	base, keyFile, graphID, demoMode, size, workers := o.base, o.keyFile, o.graphID, o.demoMode, o.size, o.workers
	failRate, stay, fps, motion, layout := o.failRate, o.stay, o.fps, o.motion, o.layout
	if *o.json || *o.limit != 0 {
		fmt.Fprintln(os.Stderr, "graphwatch: --json and --limit apply to the table commands, not the watch view (see graphwatch help)")
		os.Exit(2)
	}
	if *o.timeout <= 0 {
		fmt.Fprintln(os.Stderr, "graphwatch: --timeout must be positive")
		os.Exit(2)
	}
	clientTimeout = *o.timeout
	if *layout != "layers" && *layout != "mycelium" {
		fmt.Fprintln(os.Stderr, "graphwatch: --layout must be layers or mycelium")
		os.Exit(2)
	}
	if *graphID == "" && !*demoMode && !isTerminal(os.Stdin) {
		runHelp(nil, os.Stderr, os.Stderr)
		os.Exit(exitUsage)
	}
	if msg := watchTermProblem(isTerminal(os.Stdout), *o.noColor || os.Getenv("NO_COLOR") != "", os.Getenv("TERM") == "dumb", termSizeRaw); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(exitUsage)
	}

	// The mycelium demo needs no graph: it works a plan in-process.
	if *demoMode && *layout == "mycelium" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		v := newView()
		v.mycelium = true
		v.continuous = *motion == "continuous"
		runLocalDemo(ctx, v, *workers, *fps, *stay)
		return
	}

	if *graphID == "" && !*demoMode {
		// Bare command: a picker on a terminal, help (exit 2) otherwise.
		if !isTerminal(os.Stdin) {
			runHelp(nil, os.Stderr, os.Stderr)
			os.Exit(exitUsage)
		}
		os.Exit(runPicker(*keyFile, *base, *fps, *motion == "continuous"))
	}

	cred, source, baseURL, err := resolveFromProcess(*keyFile, *base)
	credSource = source
	if err != nil {
		fmt.Fprintln(os.Stderr, strings.TrimPrefix(err.Error(), "graphwatch: "))
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c := newClient(baseURL, cred)
	c.slowNote = os.Stderr // only until the first read: after that the alternate screen owns the terminal
	v := newView()
	v.continuous = *motion == "continuous"
	if v.mycelium = *layout == "mycelium"; v.mycelium {
		fmt.Fprintln(os.Stderr, "graphwatch: mycelium mode")
		v.say("mycelium mode", rgb{90, 210, 170}) // the alt screen hides stderr
	}

	var d *demo
	if *demoMode {
		d = &demo{c: c, size: *size, workers: *workers, failRate: *failRate,
			rng:  rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 7)),
			logf: func(s string, col rgb) { v.mu.Lock(); v.say(s, col); v.mu.Unlock() }}
		if err := d.create(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "graphwatch:", err)
			os.Exit(1)
		}
		*graphID = d.graphID
	}

	g, err := c.graph(ctx, *graphID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "graphwatch:", err)
		os.Exit(1)
	}
	c.slowNote = nil
	watchLoop(ctx, cancel, c, g, v, d, *fps, *stay, nil)
	fmt.Printf("graphwatch: %s (%s)\n", g.Slug, *graphID)
}

// watchLoop is the frame loop: read, listen, diff, draw. keys is nil unless the
// picker started the view; then q or Esc ends the loop and it reports back=true
// so the caller returns to the picker instead of exiting.
func watchLoop(ctx context.Context, cancel context.CancelFunc, c *client, g apiGraph, v *view, d *demo, fps int, stay bool, keys <-chan string) (back bool) {
	graphID := &g.ID
	stayP, fpsP := &stay, &fps
	v.title = g.Slug
	if g.DependencyEdgeType != "" {
		v.edgeT = g.DependencyEdgeType
	}

	// Refetch on every event, coalesced: a burst of events is one read.
	refresh := make(chan struct{}, 1)
	poke := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	go func() {
		t := time.NewTicker(10 * time.Second) // a safety net, not the clock
		defer t.Stop()
		for {
			ns, err1 := c.nodes(ctx, *graphID)
			es, err2 := c.edges(ctx, *graphID)
			if err1 == nil && err2 == nil {
				v.apply(ns, es)
			}
			select {
			case <-ctx.Done():
				return
			case <-refresh:
			case <-t.C:
			}
		}
	}()
	var ov *overlays
	ovPoke := func() {}
	if !noOverlays {
		ov = &overlays{}
		v.ov = ov
		ovPoke = startOverlays(ctx, c, *graphID, ov)
	}
	go c.stream(ctx, *graphID, func(ev sseEvent) {
		v.event(ev)
		if ev.Event != ":" {
			poke()
			if ov != nil {
				ov.stream(ev)
				ovPoke()
			}
		}
	}, func(s string) {
		v.setLink(s)
		if ov != nil {
			ov.link(s)
		}
	})

	// The inspector reads its own keys when no picker feeds them.
	v.insp = newInspector()
	startInspector(ctx, c, *graphID, v)
	picker := keys != nil
	if keys == nil {
		if undo, err := sttyRaw(); err == nil {
			defer undo()
			keys = readKeys(ctx, os.Stdin)
		}
	}

	var wg sync.WaitGroup
	if d != nil {
		finished := func() bool { _, ok := v.completed(); return ok && d.planned.Load() }
		wg.Add(1)
		go func() { defer wg.Done(); d.plan(ctx) }()
		for i := 0; i < d.workers; i++ {
			wg.Add(1)
			go func(i int) { defer wg.Done(); d.work(ctx, i, finished) }(i + 1)
		}
	}

	// The terminal: alternate screen, no cursor, restored on every exit path.
	os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J")
	restore := func() { os.Stdout.WriteString("\x1b[0m\x1b[?25h\x1b[?1049l") }
	defer restore()

	w, h := termSize()
	sizeT := time.NewTicker(time.Second)
	defer sizeT.Stop()
	frameT := time.NewTicker(time.Second / time.Duration(max(*fpsP, 1)))
	defer frameT.Stop()
	last := time.Now()
	var scr screen
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case k := <-keys:
			if k != "q" && k != "ctrl-c" && v.insp.key(k) {
				continue
			}
			if k == "q" || k == "esc" || k == "ctrl-c" {
				back = picker && k != "ctrl-c"
				break loop
			}
		case <-sizeT.C:
			if nw, nh := termSize(); nw != w || nh != h {
				w, h = nw, nh
				scr.reset()
			}
		case now := <-frameT.C:
			dt := now.Sub(last).Seconds()
			last = now
			if out := scr.diff(v.frame(w, h, dt)); out != "" {
				os.Stdout.WriteString(out)
			}
			if at, ok := v.completed(); ok && !*stayP && (d == nil || d.planned.Load()) && time.Since(at) > 6*time.Second {
				break loop
			}
		}
	}
	cancel()
	wg.Wait()
	restore()
	return back
}

// termSize asks stty, so the command needs nothing outside the standard
// library. Falls back to 100×32.
func termSize() (int, int) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	if out, err := cmd.Output(); err == nil {
		if f := strings.Fields(string(out)); len(f) == 2 {
			h, _ := strconv.Atoi(f[0])
			w, _ := strconv.Atoi(f[1])
			if w > 20 && h > 10 {
				return w, h
			}
		}
	}
	return 100, 32
}

// minCols and minRows are the smallest terminal the watch view draws for.
const (
	minCols = 80
	minRows = 24
)

// watchTermProblem says why the watch view must not draw, or "" when it may.
// size reports the terminal's real size and ok=false when it cannot be read, in
// which case the size check is skipped rather than guessed.
func watchTermProblem(stdoutTTY, noColor, dumb bool, size func() (w, h int, ok bool)) string {
	switch {
	case !stdoutTTY:
		return "graphwatch: the watch view draws on a terminal and stdout is not one; use a command such as `graphwatch graphs` for text output"
	case noColor:
		return "graphwatch: the watch view is drawn in colour and cannot run with NO_COLOR or --no-color; use a command such as `graphwatch graphs --no-color`"
	case dumb:
		return "graphwatch: the watch view needs cursor control and TERM=dumb has none; use a command such as `graphwatch graphs`"
	}
	if w, h, ok := size(); ok && (w < minCols || h < minRows) {
		return fmt.Sprintf("graphwatch needs a terminal of at least %dx%d (this one is %dx%d)", minCols, minRows, w, h)
	}
	return ""
}

// termSizeRaw is the terminal's actual size, with no fallback.
func termSizeRaw() (w, h int, ok bool) {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return 0, 0, false
	}
	h, _ = strconv.Atoi(f[0])
	w, _ = strconv.Atoi(f[1])
	return w, h, w > 0 && h > 0
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
