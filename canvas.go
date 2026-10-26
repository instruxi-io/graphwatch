package main

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// rgb is a 24-bit terminal colour.
type rgb struct{ r, g, b uint8 }

func (c rgb) mix(o rgb, t float64) rgb {
	t = clamp01(t)
	l := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return rgb{l(c.r, o.r), l(c.g, o.g), l(c.b, o.b)}
}

func (c rgb) scale(t float64) rgb { return rgb{}.mix(c, t) }

// quantize snaps each channel to one of 16 levels at the point of output.
// The animation computes colours continuously (heat decays, pulses follow a
// sine), so unquantized every frame repaints cells whose colour moved by one
// unit and the set of styles a terminal must keep is open-ended; snapped, a
// cooling node stops changing once it settles and at most 16³ styles exist.
func (c rgb) quantize() rgb {
	q := func(v uint8) uint8 { return uint8((int(v) + 8) / 17 * 17) }
	return rgb{q(c.r), q(c.g), q(c.b)}
}

func clamp01(t float64) float64 {
	switch {
	case t < 0:
		return 0
	case t > 1:
		return 1
	}
	return t
}

// braille maps a dot at (x, y) inside a 2×4 cell to its bit in U+2800.
var braille = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// canvas is a braille pixel grid (2×4 dots per terminal cell) with a text layer
// on top. Each cell takes the colour of its brightest dot, so a hot particle
// crossing a cold edge wins the cell for that frame.
type canvas struct {
	w, h  int // cells
	dots  []uint8
	dcol  []rgb
	dpri  []float64
	text  []rune
	tcol  []rgb
	tbold []bool
}

func newCanvas(w, h int) *canvas {
	n := w * h
	return &canvas{w: w, h: h, dots: make([]uint8, n), dcol: make([]rgb, n), dpri: make([]float64, n),
		text: make([]rune, n), tcol: make([]rgb, n), tbold: make([]bool, n)}
}

// dot sets the braille pixel (px, py); pixel space is 2w × 4h.
func (c *canvas) dot(px, py int, col rgb, pri float64) {
	if px < 0 || py < 0 || px >= c.w*2 || py >= c.h*4 {
		return
	}
	i := (py/4)*c.w + px/2
	c.dots[i] |= braille[py%4][px%2]
	if pri >= c.dpri[i] {
		c.dpri[i], c.dcol[i] = pri, col
	}
}

// put writes text at a cell. Text always wins over dots.
func (c *canvas) put(cx, cy int, s string, col rgb, bold bool) {
	if cy < 0 || cy >= c.h {
		return
	}
	for _, r := range s {
		if cx >= 0 && cx < c.w {
			i := cy*c.w + cx
			c.text[i], c.tcol[i], c.tbold[i] = r, col, bold
		}
		cx++
	}
}

// putCentered writes s centred on cx, clipped to max runes.
func (c *canvas) putCentered(cx, cy int, s string, max int, col rgb, bold bool) {
	if max <= 0 {
		return
	}
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		if max > 1 {
			s = string(r[:max-1]) + "…"
		} else {
			s = string(r[:max])
		}
	}
	c.put(cx-utf8.RuneCountInString(s)/2, cy, s, col, bold)
}

// render emits the frame, changing colour only when it changes.
func (c *canvas) render(b *strings.Builder) {
	var last rgb
	var lastBold, have bool
	for y := 0; y < c.h; y++ {
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(y + 1))
		b.WriteString(";1H")
		for x := 0; x < c.w; x++ {
			i := y*c.w + x
			r, col, bold := ' ', rgb{}, false
			switch {
			case c.text[i] != 0:
				r, col, bold = c.text[i], c.tcol[i], c.tbold[i]
			case c.dots[i] != 0:
				r, col = rune(0x2800+int(c.dots[i])), c.dcol[i]
			}
			col = col.quantize()
			if r != ' ' && (!have || col != last || bold != lastBold) {
				if bold {
					b.WriteString("\x1b[1;38;2;")
				} else {
					b.WriteString("\x1b[22;38;2;")
				}
				b.WriteString(strconv.Itoa(int(col.r)))
				b.WriteByte(';')
				b.WriteString(strconv.Itoa(int(col.g)))
				b.WriteByte(';')
				b.WriteString(strconv.Itoa(int(col.b)))
				b.WriteByte('m')
				last, lastBold, have = col, bold, true
			}
			b.WriteRune(r)
		}
	}
	b.WriteString("\x1b[0m")
}

// thermal is an inferno-like ramp: cold violet through red and orange to a
// near-white yellow. Heat is work: a node or edge is as hot as it is busy.
var thermalStops = []struct {
	t float64
	c rgb
}{
	{0.00, rgb{24, 14, 58}},
	{0.25, rgb{110, 30, 120}},
	{0.50, rgb{196, 58, 82}},
	{0.70, rgb{240, 112, 36}},
	{0.86, rgb{252, 186, 30}},
	{1.00, rgb{255, 250, 190}},
}

func thermal(t float64) rgb {
	t = clamp01(t)
	for i := 1; i < len(thermalStops); i++ {
		if t <= thermalStops[i].t {
			a, b := thermalStops[i-1], thermalStops[i]
			return a.c.mix(b.c, (t-a.t)/(b.t-a.t))
		}
	}
	return thermalStops[len(thermalStops)-1].c
}
