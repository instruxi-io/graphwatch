package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

// The bare command: a keyboard picker over the graphs you can see. Key
// handling is a pure function so it tests without a terminal; everything that
// touches the tty lives in runPicker below. Read-only: it lists and then hands
// one graph to the watch or tail view.

type pickerRow struct {
	ID, Slug, Name, State string
	Nodes                 int // -1 when unknown
	Counts                string
}

// pickerState is the whole picker. Update returns a new value.
type pickerState struct {
	rows      []pickerRow
	filter    string
	filtering bool // typing into the filter
	cursor    int  // index into visible()
	height    int  // list rows that fit on screen
	// Result of the last key: what the caller should do next.
	action  string // "", "watch", "mycelium", "tail", "refresh", "quit"
	chosen  pickerRow
	message string
}

func (s pickerState) visible() []pickerRow {
	if s.filter == "" {
		return s.rows
	}
	f := strings.ToLower(s.filter)
	var out []pickerRow
	for _, r := range s.rows {
		if strings.Contains(strings.ToLower(r.Slug+" "+r.Name+" "+r.ID), f) {
			out = append(out, r)
		}
	}
	return out
}

// pickerUpdate applies one key. Keys are "up", "down", "enter", "esc",
// "backspace", "ctrl-c" or the typed character.
func pickerUpdate(s pickerState, key string) pickerState {
	s.action = ""
	vis := s.visible()
	pick := func(action string) pickerState {
		if len(vis) == 0 {
			return s
		}
		s.chosen, s.action = vis[s.cursor], action
		return s
	}
	clamp := func() {
		if n := len(s.visible()); s.cursor >= n {
			s.cursor = max(n-1, 0)
		}
	}
	if key == "ctrl-c" {
		s.action = "quit"
		return s
	}
	if s.filtering {
		switch {
		case key == "esc":
			s.filtering, s.filter = false, ""
		case key == "enter":
			s.filtering = false
		case key == "backspace":
			if r := []rune(s.filter); len(r) > 0 {
				s.filter = string(r[:len(r)-1])
			}
			s.cursor = 0
		case key == "up" || key == "down":
			// fall through to movement below
		case len([]rune(key)) == 1:
			s.filter += key
			s.cursor = 0
		}
		if key != "up" && key != "down" {
			clamp()
			return s
		}
	}
	switch key {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(vis)-1 {
			s.cursor++
		}
	case "/":
		s.filtering = true
	case "enter":
		return pick("watch")
	case "m":
		return pick("mycelium")
	case "t":
		return pick("tail")
	case "r":
		s.action = "refresh"
	case "q", "esc":
		if key == "esc" && s.filter != "" {
			s.filter = ""
			s.cursor = 0
			break
		}
		s.action = "quit"
	}
	return s
}

// renderPicker draws the list into lines of at most w cells, h lines.
func renderPicker(s pickerState, w, h int) []string {
	lines := []string{"graphwatch: pick a graph"}
	vis := s.visible()
	rows := max(h-4, 1)
	top := 0
	if s.cursor >= rows {
		top = s.cursor - rows + 1
	}
	lines = append(lines, fmt.Sprintf("  %-22s %-26s %-10s %s", "SLUG", "NAME", "STATE", "NODES"))
	for i := top; i < len(vis) && i < top+rows; i++ {
		r := vis[i]
		n := "-"
		if r.Nodes >= 0 {
			n = fmt.Sprint(r.Nodes)
			if r.Counts != "" {
				n += " (" + r.Counts + ")"
			}
		}
		mark := "  "
		if i == s.cursor {
			mark = "> "
		}
		lines = append(lines, mark+fmt.Sprintf("%-22s %-26s %-10s %s", cut(nodeDash(r.Slug), 22), cut(r.Name, 26), cut(nodeDash(r.State), 10), n))
	}
	if len(vis) == 0 {
		lines = append(lines, "  (no graphs match)")
	}
	foot := "↑/↓ j/k move  / filter  enter watch  m mycelium  t tail  r refresh  q quit"
	if s.filtering {
		foot = "filter: " + s.filter + "_   (enter keeps, esc clears)"
	} else if s.filter != "" {
		foot = "filter: " + s.filter + "   " + foot
	}
	if s.message != "" {
		foot = s.message + "  " + foot
	}
	for len(lines) < h-1 {
		lines = append(lines, "")
	}
	lines = append(lines, foot)
	for i, l := range lines {
		lines[i] = cut(l, w)
	}
	return lines
}

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:max(n-1, 0)]) + "…"
	}
	return s
}

// loadPickerRows lists graphs and reads each one's summary for the roll-up
// state and node counts. A summary that fails leaves the row with dashes.
func loadPickerRows(ctx context.Context, c *client) ([]pickerRow, error) {
	graphs, err := c.graphsList(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]pickerRow, 0, len(graphs))
	for _, g := range graphs {
		if g.ArchivedAt != "" {
			continue
		}
		rows = append(rows, pickerRow{ID: g.ID, Slug: g.Slug, Name: g.Name, State: g.State, Nodes: -1})
	}
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(r *pickerRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sum, err := c.graphSummary(ctx, r.ID)
			if err != nil {
				return
			}
			if r.State == "" {
				r.State = sum.State
			}
			r.Nodes, r.Counts = sum.total(), sum.counts()
		}(&rows[i])
	}
	wg.Wait()
	return rows, nil
}

// readKeys turns stdin bytes into key names until the context ends.
func readKeys(ctx context.Context, in io.Reader) <-chan string {
	ch := make(chan string, 16)
	go func() {
		buf := make([]byte, 16)
		for {
			n, err := in.Read(buf)
			if err != nil || n == 0 {
				close(ch)
				return
			}
			for _, k := range parseKeys(buf[:n]) {
				select {
				case ch <- k:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch
}

func parseKeys(b []byte) []string {
	var out []string
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == 0x1b:
			if i+2 < len(b) && b[i+1] == '[' {
				switch b[i+2] {
				case 'A':
					out = append(out, "up")
				case 'B':
					out = append(out, "down")
				case 'C':
					out = append(out, "right")
				case 'D':
					out = append(out, "left")
				}
				i += 2
			} else {
				out = append(out, "esc")
			}
		case c == '\r' || c == '\n':
			out = append(out, "enter")
		case c == 0x7f || c == 0x08:
			out = append(out, "backspace")
		case c == 3:
			out = append(out, "ctrl-c")
		case c >= 0x20 && c < 0x7f:
			out = append(out, string(rune(c)))
		}
	}
	return out
}

// sttyRaw switches the tty to unbuffered, no-echo input and returns the undo.
// It saves the current settings so the restore is exact.
func sttyRaw() (restore func(), err error) {
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	saved, err := get.Output()
	if err != nil {
		return func() {}, err
	}
	set := exec.Command("stty", "-icanon", "-echo")
	set.Stdin = os.Stdin
	if err := set.Run(); err != nil {
		return func() {}, err
	}
	return func() {
		r := exec.Command("stty", strings.TrimSpace(string(saved)))
		r.Stdin = os.Stdin
		_ = r.Run()
	}, nil
}

const (
	altOn  = "\x1b[?1049h\x1b[?25l\x1b[2J"
	altOff = "\x1b[0m\x1b[?25h\x1b[?1049l"
)

// runPicker is the interactive loop. It returns the exit code.
func runPicker(keyFile, base string, fps int, continuous bool) int {
	cred, _, baseURL, err := resolveFromProcess(keyFile, base)
	if err != nil {
		fmt.Fprintln(os.Stderr, strings.TrimPrefix(err.Error(), "graphwatch: "))
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := newClient(baseURL, cred)

	undo, err := sttyRaw()
	defer undo() // every exit path, including panics
	if err != nil {
		fmt.Fprintln(os.Stderr, "graphwatch: cannot read keys from this terminal:", err)
		return exitRuntime
	}
	keys := readKeys(ctx, os.Stdin)
	os.Stdout.WriteString(altOn)
	defer os.Stdout.WriteString(altOff)

	st := pickerState{message: "loading…"}
	reload := func() {
		draw(st)
		rows, err := loadPickerRows(ctx, c)
		if err != nil {
			st.message = "error: " + strings.TrimPrefix(reportMsg(err), "graphwatch: ")
			return
		}
		st.rows, st.message = rows, ""
		st = pickerUpdate(st, "") // re-clamp
	}
	reload()
	for {
		draw(st)
		select {
		case <-ctx.Done():
			return exitOK
		case k, ok := <-keys:
			if !ok {
				return exitOK
			}
			st = pickerUpdate(st, k)
			switch st.action {
			case "quit":
				return exitOK
			case "refresh":
				st.message = "loading…"
				reload()
			case "watch", "mycelium":
				os.Stdout.WriteString(altOff)
				launchWatch(ctx, c, st.chosen, st.action == "mycelium", fps, continuous, keys)
				os.Stdout.WriteString(altOn)
				st.message = ""
			case "tail":
				os.Stdout.WriteString(altOff)
				launchTail(ctx, st.chosen, keyFile, base, keys)
				os.Stdout.WriteString(altOn)
				st.message = ""
			}
		}
	}
}

func reportMsg(err error) string {
	var b strings.Builder
	reportError(&b, err)
	return strings.TrimSpace(b.String())
}

func draw(s pickerState) {
	w, h := termSize()
	var b strings.Builder
	b.WriteString("\x1b[H")
	for _, l := range renderPicker(s, w, h) {
		b.WriteString(l + "\x1b[K\r\n")
	}
	b.WriteString("\x1b[J")
	os.Stdout.WriteString(b.String())
}

func launchWatch(ctx context.Context, c *client, row pickerRow, mycelium bool, fps int, continuous bool, keys <-chan string) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g, err := c.graph(wctx, row.ID)
	if err != nil {
		return
	}
	v := newView()
	v.continuous = continuous
	v.mycelium = mycelium
	v.title = g.Slug
	if g.DependencyEdgeType != "" {
		v.edgeT = g.DependencyEdgeType
	}
	// stay: a finished graph returns to the picker on q, not on its own.
	watchLoop(wctx, cancel, c, g, v, nil, fps, true, keys)
}

// launchTail runs the tail command until q, Esc or Ctrl-C.
func launchTail(ctx context.Context, row pickerRow, keyFile, base string, keys <-chan string) {
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			select {
			case <-tctx.Done():
				return
			case k, ok := <-keys:
				if !ok || k == "q" || k == "esc" || k == "ctrl-c" {
					cancel()
					return
				}
			}
		}
	}()
	args := []string{}
	if keyFile != "" {
		args = append(args, "--api-key-file", keyFile)
	}
	if base != "" {
		args = append(args, "--base", base)
	}
	runCommand(tctx, commands["tail"], append(args, row.ID), os.Stdout, os.Stderr)
}
