package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderTableAligns(t *testing.T) {
	var b bytes.Buffer
	renderTable(&b, []string{"ID", "TITLE"}, [][]string{
		{"1", "short"},
		{"long-id", strings.Repeat("x", 100)},
	})
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: %q", lines)
	}
	col := strings.Index(lines[0], "TITLE")
	for _, l := range lines[1:] {
		if strings.IndexAny(l[col:col+1], " ") == 0 || l[col-2:col] != "  " {
			t.Errorf("column misaligned in %q", l)
		}
	}
	if !strings.HasSuffix(lines[2], "…") || len([]rune(lines[2])) > col+maxCell {
		t.Errorf("long cell not truncated: %q", lines[2])
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Error("plain renderTable must not emit colour")
	}
}

func TestTableColorOnlyOnTerminal(t *testing.T) {
	var b bytes.Buffer
	e := &cliEnv{out: &b} // not a terminal
	e.table([]string{"A"}, [][]string{{"1"}})
	if strings.Contains(b.String(), "\x1b") {
		t.Error("colour on a non-terminal")
	}
	e = &cliEnv{out: &b, isTTY: true}
	t.Setenv("NO_COLOR", "1")
	if e.useColor() {
		t.Error("NO_COLOR ignored")
	}
	t.Setenv("NO_COLOR", "")
	if !e.useColor() {
		t.Error("no colour on a terminal")
	}
	e.noColor = true
	if e.useColor() {
		t.Error("--no-color ignored")
	}
}

func TestRenderJSONPrintsData(t *testing.T) {
	var b bytes.Buffer
	if err := renderJSON(&b, json.RawMessage(`[{"id":"a","n":1}]`)); err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"id\": \"a\",\n    \"n\": 1\n  }\n]\n"
	if b.String() != want {
		t.Fatalf("got %q", b.String())
	}
	b.Reset()
	if err := renderJSON(&b, map[string]int{"x": 1}); err != nil || !strings.Contains(b.String(), "\"x\": 1") {
		t.Fatalf("map: %v %q", err, b.String())
	}
}
