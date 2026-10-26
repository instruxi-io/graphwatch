package main

import (
	"context"
	"strings"
	"testing"
)

func testRows() []pickerRow {
	return []pickerRow{
		{ID: "id-a", Slug: "alpha", Name: "Alpha plan", State: "active", Nodes: 3, Counts: "done 1, active 2"},
		{ID: "id-b", Slug: "beta", Name: "Beta plan", State: "idle", Nodes: 0},
		{ID: "id-c", Slug: "gamma", Name: "Gamma", State: "complete", Nodes: 5, Counts: "done 5"},
	}
}

func press(s pickerState, keys ...string) pickerState {
	for _, k := range keys {
		s = pickerUpdate(s, k)
	}
	return s
}

func TestPickerKeyNavigation(t *testing.T) {
	s := pickerState{rows: testRows()}
	s = press(s, "down", "j")
	if s.cursor != 2 {
		t.Fatalf("cursor=%d want 2", s.cursor)
	}
	s = press(s, "down") // stops at the end
	if s.cursor != 2 {
		t.Fatalf("cursor=%d want 2 at end", s.cursor)
	}
	s = press(s, "up", "k", "k")
	if s.cursor != 0 {
		t.Fatalf("cursor=%d want 0", s.cursor)
	}
	if s = press(s, "r"); s.action != "refresh" {
		t.Fatalf("r: action=%q", s.action)
	}
}

func TestPickerFilterTyping(t *testing.T) {
	s := press(pickerState{rows: testRows()}, "/", "g", "a")
	if !s.filtering || s.filter != "ga" {
		t.Fatalf("filtering=%v filter=%q", s.filtering, s.filter)
	}
	if v := s.visible(); len(v) != 1 || v[0].Slug != "gamma" {
		t.Fatalf("visible=%v", v)
	}
	// q is a letter while typing, not quit.
	if s = press(s, "q"); s.action != "" || s.filter != "gaq" {
		t.Fatalf("q while filtering: action=%q filter=%q", s.action, s.filter)
	}
	s = press(s, "backspace", "backspace", "backspace")
	if s.filter != "" || len(s.visible()) != 3 {
		t.Fatalf("filter=%q visible=%d", s.filter, len(s.visible()))
	}
	s = press(s, "b", "enter")
	if s.filtering || s.filter != "b" || s.action != "" {
		t.Fatalf("enter keeps filter: %+v", s)
	}
	if s = press(s, "esc"); s.filter != "" || s.action == "quit" {
		t.Fatalf("esc clears filter first: %+v", s)
	}
}

func TestPickerEnterSelectsGraph(t *testing.T) {
	s := press(pickerState{rows: testRows()}, "down", "enter")
	if s.action != "watch" || s.chosen.ID != "id-b" {
		t.Fatalf("action=%q chosen=%q", s.action, s.chosen.ID)
	}
	if s = press(s, "m"); s.action != "mycelium" || s.chosen.ID != "id-b" {
		t.Fatalf("m: %q %q", s.action, s.chosen.ID)
	}
	if s = press(s, "t"); s.action != "tail" {
		t.Fatalf("t: %q", s.action)
	}
	// Selection follows the filter, not the unfiltered index.
	s = press(pickerState{rows: testRows()}, "/", "g", "enter", "enter")
	if s.chosen.ID != "id-c" {
		t.Fatalf("filtered chosen=%q", s.chosen.ID)
	}
	if s = press(pickerState{}, "enter"); s.action != "" {
		t.Fatalf("empty list must not select: %q", s.action)
	}
}

func TestPickerRendersGraphList(t *testing.T) {
	f := newFakeAPI(t, map[string]any{
		"/graphs": []map[string]any{
			{"id": "id-a", "slug": "alpha", "name": "Alpha plan"},
			{"id": "id-b", "slug": "beta", "name": "Beta plan"},
		},
		"/graphs/id-a/summary": map[string]any{"state": "active", "by_status": map[string]int{"done": 1, "active": 2}},
		"/graphs/id-b/summary": map[string]any{"state": "complete", "by_status": map[string]int{"done": 4}},
	})
	c := newClient(f.URL, apiKey("test-key"))
	rows, err := loadPickerRows(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(renderPicker(pickerState{rows: rows}, 100, 12), "\n")
	for _, want := range []string{"> alpha", "Alpha plan", "active", "3 (active 2, done 1)", "beta", "complete", "4 (done 4)"} {
		if !strings.Contains(out, want) {
			t.Errorf("screen lacks %q:\n%s", want, out)
		}
	}
	f.onlyReads(t)
	if strings.Contains(out, "test-key") {
		t.Fatal("credential on screen")
	}
}

func TestPickerQuitRestores(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl-c"} {
		if s := press(pickerState{rows: testRows()}, k); s.action != "quit" {
			t.Errorf("%s: action=%q", k, s.action)
		}
	}
	got := parseKeys([]byte("\x1b[A\x1b[Bj\r\x7f\x1b"))
	want := []string{"up", "down", "j", "enter", "backspace", "esc"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("parseKeys=%v", got)
	}
	// The terminal is left through altOff: cursor back, alt screen off.
	if !strings.Contains(altOff, "?25h") || !strings.Contains(altOff, "?1049l") {
		t.Fatal("altOff does not restore the terminal")
	}
}
