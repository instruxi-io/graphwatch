package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// isTerminal reports whether w is a character device, without x/term.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// useColor: only on a terminal, and never with --no-color or NO_COLOR.
func (e *cliEnv) useColor() bool {
	return e.isTTY && !e.noColor && os.Getenv("NO_COLOR") == ""
}

const maxCell = 60

// clean makes a string from the API safe to print on a terminal. Titles,
// reasons, reports and evidence are written by other accounts and agents, so a
// raw ESC or BEL in one could retitle the window, clear the screen or ring the
// bell. Every control character except tab and newline is shown as an escape
// such as \x1b instead of being sent. Tools that print API text go through
// this one function.
func clean(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var sb strings.Builder
	for _, r := range s {
		switch {
		case !isControl(r):
			sb.WriteRune(r)
		case r < 0x100 && r < 0x80:
			fmt.Fprintf(&sb, "\\x%02x", r)
		default:
			fmt.Fprintf(&sb, "\\u%04x", r)
		}
	}
	return sb.String()
}

// isControl is C0, DEL and C1, except tab and newline.
func isControl(r rune) bool {
	if r == '\t' || r == '\n' {
		return false
	}
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// renderTable prints column-aligned rows. A cell longer than maxCell is cut
// with an ellipsis so one long title cannot push the other columns off screen.
// Colour is limited to the header (bold) and applied after padding, so escape
// bytes never skew alignment.
func renderTable(w io.Writer, headers []string, rows [][]string) {
	renderTableColor(w, headers, rows, false)
}

func renderTableColor(w io.Writer, headers []string, rows [][]string, color bool) {
	cut := func(s string) string {
		s = strings.Join(strings.Fields(clean(s)), " ")
		if utf8.RuneCountInString(s) <= maxCell {
			return s
		}
		return string([]rune(s)[:maxCell-1]) + "…"
	}
	width := make([]int, len(headers))
	for i, h := range headers {
		width[i] = utf8.RuneCountInString(h)
	}
	cells := make([][]string, len(rows))
	for r, row := range rows {
		cells[r] = make([]string, len(headers))
		for i := range headers {
			if i < len(row) {
				cells[r][i] = cut(row[i])
			}
			if n := utf8.RuneCountInString(cells[r][i]); n > width[i] {
				width[i] = n
			}
		}
	}
	line := func(vals []string, bold bool) {
		var sb strings.Builder
		for i, v := range vals {
			pad := strings.Repeat(" ", width[i]-utf8.RuneCountInString(v))
			if i == len(vals)-1 {
				pad = ""
			}
			if bold && color {
				sb.WriteString("\x1b[1m" + v + "\x1b[0m" + pad)
			} else {
				sb.WriteString(v + pad)
			}
			if i < len(vals)-1 {
				sb.WriteString("  ")
			}
		}
		fmt.Fprintln(w, sb.String())
	}
	line(headers, true)
	for _, c := range cells {
		line(c, false)
	}
}

// table prints through the env, so colour follows the terminal and flags.
func (e *cliEnv) table(headers []string, rows [][]string) {
	renderTableColor(e.out, headers, rows, e.useColor())
}

// renderJSON prints v re-indented. v is the API's `data` payload (a
// json.RawMessage, []byte or any marshalable value), so `--json | jq` sees the
// payload without the envelope.
func renderJSON(w io.Writer, v any) error {
	var b []byte
	switch t := v.(type) {
	case json.RawMessage:
		b = t
	case []byte:
		b = t
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}
