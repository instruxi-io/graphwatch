package main

import (
	"fmt"
	"strings"
	"time"
)

// inspectorLines composes the panel as plain text, width cells wide at most.
// It is a pure function of its state so it tests without a terminal.
func inspectorLines(st inspState, width int) []string {
	var out []string
	add := func(format string, a ...any) {
		out = append(out, clip(fmt.Sprintf(format, a...), width))
	}
	n := st.Node
	if n.Key == "" && st.Loading {
		return []string{clip("loading…", width)}
	}
	add("%s  %s", n.Key, n.Title)
	state := "status " + n.Status
	if n.WorkState != "" {
		state += " · work_state " + n.WorkState
	}
	if st.Held {
		state += " · HELD (verifying or open review)"
	}
	add("%s", state)
	d := parseInspNodeData(n.Data)
	if d.Tier != "" {
		src := d.TierSource
		if src == "" {
			src = "unknown"
		}
		add("tier %s (source %s)", d.Tier, src)
	} else {
		add("tier not set")
	}

	switch {
	case st.RunErr != "":
		add("run: unavailable (%s)", st.RunErr)
	case st.Run == nil:
		add("run: none yet")
	default:
		r := st.Run
		line := fmt.Sprintf("run attempt %d · %s · runner %s", r.Attempt, r.Status, orDash(r.Runner))
		if r.Status == "running" && !st.Now.IsZero() {
			line += " · started " + ageShort(st.Now.Sub(r.StartedAt)) + " ago"
		}
		add("%s", line)
		if v := r.Verification; v != nil {
			c := "n/a"
			if v.Confidence != nil {
				c = fmt.Sprintf("%.2f", *v.Confidence)
			}
			add("verdict %s · confidence %s", orDash(v.State), c)
			for i, cr := range v.Criteria {
				mark := "?"
				if cr.Noul != nil {
					mark = map[bool]string{true: "MET", false: "NOT MET"}[*cr.Noul >= 0.5]
				}
				add("  %d. %s  %s", i+1, mark, cr.Text)
			}
		} else {
			add("verdict none")
		}
		if va := r.Validation; va != nil {
			q := "votes " + va.State
			if va.Tally != nil {
				q += fmt.Sprintf(" (accept %d · reject %d · undecided %d)", va.Tally.Accept, va.Tally.Reject, va.Tally.Undecided)
			}
			add("%s", q)
		} else if len(st.Votes) > 0 {
			add("votes %d cast", len(st.Votes))
		}
		add("evidence %d item(s)", len(r.Evidence))
	}

	switch {
	case st.RouteErr != "":
		add("route: unavailable (%s)", st.RouteErr)
	case st.Route == nil:
		add("route: loading…")
	case st.Route.RouteUndecided:
		add("route undecided%s", suffix(" · ", st.Route.Reason))
	default:
		r := st.Route
		add("route tier %s · model %s", orDash(r.Tier), orDash(r.Model))
		if r.Reason != "" {
			add("  reason %s", r.Reason)
		}
	}

	if len(d.Acceptance) > 0 {
		add("acceptance")
		for i, a := range d.Acceptance {
			add("  %d. %s", i+1, a)
		}
	}
	return out
}

func clip(s string, width int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if width > 0 && len(r) > width {
		return string(r[:max(width-1, 0)]) + "…"
	}
	return string(r)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func suffix(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

func ageShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// drawInspector paints the selection ring, the held markers and, when open,
// the panel over the bottom rows. It draws nothing at all when there is no
// inspector, so the unchanged frame stays byte-identical.
func (v *view) drawInspector(c *canvas, w, h int, ids []string) {
	in := v.insp
	if in == nil {
		return
	}
	amber, white := rgb{250, 190, 60}, rgb{240, 242, 250}
	pos := map[string][2]float64{}
	in.mu.Lock()
	sel, held := in.sel, in.held
	in.mu.Unlock()
	for _, id := range ids {
		vn := v.vis[id]
		pos[id] = [2]float64{vn.x, vn.y}
		cx, cy := int(vn.x+0.5), int(vn.y+0.5)
		if isHeld(v.nodes[id].status, held[id]) {
			c.put(cx-1, cy, "⟨", amber, true)
			c.put(cx+1, cy, "⟩", amber, true)
		}
		if id == sel {
			c.put(cx-2, cy, "[", white, true)
			c.put(cx+2, cy, "]", white, true)
		}
	}
	in.mu.Lock()
	in.pos = pos
	in.mu.Unlock()
	st, open := in.snapshot()
	if !open {
		return
	}
	st.Now = v.clock()
	lines := inspectorLines(st, w-4)
	top := max(3, h-len(lines)-2)
	for y := top - 1; y < h; y++ {
		c.put(0, y, strings.Repeat(" ", w), rgb{}, false)
	}
	c.put(1, top-1, strings.Repeat("─", w-2), rgb{70, 72, 90}, false)
	for i, l := range lines {
		if top+i >= h {
			break
		}
		c.put(2, top+i, l, rgb{200, 206, 222}, i == 0)
	}
}
