package main

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// screen remembers what the terminal is showing and turns each full frame
// from canvas.render into only the cells that changed. A full repaint at 30
// fps was ~15 KB a frame for a 100×32 terminal while the median frame changed
// 29 cells; VS Code's integrated terminal grew by ~0.3 GB a minute under that
// stream and recovered when it stopped (measured 2026-10-01). Unchanged
// frames now cost nothing.
type screen struct {
	w, h  int
	cells []cell
	pen   string // the SGR parameters the terminal is currently drawing with
	valid bool
}

// cell is one terminal cell: a rune and the SGR parameters it was drawn
// with. A blank's style is irrelevant (styles never set a background), so it
// is stored as "".
type cell struct {
	r  rune
	st string
}

// reset forgets the terminal's contents; the next diff clears and redraws.
func (s *screen) reset() { s.valid = false }

// diff returns the bytes that turn the current screen into frame.
func (s *screen) diff(frame string) string {
	next, w, h := parseFrame(frame)
	var b strings.Builder
	if !s.valid || w != s.w || h != s.h {
		b.WriteString("\x1b[0m\x1b[2J")
		s.w, s.h, s.pen, s.valid = w, h, "", true
		s.cells = make([]cell, w*h)
		for i := range s.cells {
			s.cells[i] = cell{r: ' '}
		}
	}
	cx, cy := -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			nc := next[i]
			if nc == s.cells[i] {
				continue
			}
			if cx != x || cy != y {
				b.WriteString("\x1b[")
				b.WriteString(strconv.Itoa(y + 1))
				b.WriteByte(';')
				b.WriteString(strconv.Itoa(x + 1))
				b.WriteByte('H')
			}
			if nc.r != ' ' && nc.st != s.pen {
				b.WriteString("\x1b[")
				b.WriteString(nc.st)
				b.WriteByte('m')
				s.pen = nc.st
			}
			b.WriteRune(nc.r)
			cx, cy = x+1, y
			s.cells[i] = nc
		}
	}
	return b.String()
}

// parseFrame reads canvas.render's output back into cells. render owns the
// format: each row starts with "\x1b[<row>;1H", styles are SGR sequences, and
// the frame ends with a reset. Anything else is a rune.
func parseFrame(f string) ([]cell, int, int) {
	var rows [][]cell
	var row []cell
	st := ""
	for i := 0; i < len(f); {
		if f[i] == 0x1b && i+1 < len(f) && f[i+1] == '[' {
			j := i + 2
			for j < len(f) && (f[j] == ';' || (f[j] >= '0' && f[j] <= '9')) {
				j++
			}
			if j >= len(f) {
				break
			}
			params := f[i+2 : j]
			switch f[j] {
			case 'H':
				if row != nil {
					rows = append(rows, row)
				}
				row = []cell{}
			case 'm':
				st = params
			}
			i = j + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(f[i:])
		i += n
		if r == ' ' {
			row = append(row, cell{r: ' '})
		} else {
			row = append(row, cell{r: r, st: st})
		}
	}
	if row != nil {
		rows = append(rows, row)
	}
	h, w := len(rows), 0
	if h > 0 {
		w = len(rows[0])
	}
	out := make([]cell, w*h)
	for y, r := range rows {
		for x := 0; x < w; x++ {
			if x < len(r) {
				out[y*w+x] = r[x]
			} else {
				out[y*w+x] = cell{r: ' '}
			}
		}
	}
	return out, w, h
}
