package main

import (
	"context"
	"strings"
	"testing"
)

func ip(n int) *int { return &n }

func fullState() headerState {
	rec := &recruitingInfo{}
	rec.Recruiting.Open, rec.Recruiting.Audience, rec.Recruiting.MaxParallel = true, "workspace", 3
	return headerState{
		Counts: map[string]int{"looking_for_work": 4, "claimed": 2, "looking_for_validation": 1, "held": 0},
		Review: ip(0), Epoch: ip(2), Epochs: ip(2), Recruiting: rec, Stream: "live", Cursor: "1235@snap",
	}
}

func TestHeaderLineSegments(t *testing.T) {
	line := headerLine(fullState(), 400)
	for _, want := range []string{"epoch 2", "4 looking for work", "2 claimed", "1 looking for validation", "0 held", "recruiting workspace (max 3)", "live seq 1235"} {
		if !strings.Contains(line, want) {
			t.Errorf("%q missing from %q", want, line)
		}
	}
	s := fullState()
	s.Epochs = ip(5)
	if l := headerLine(s, 400); !strings.Contains(l, "epoch 2 of 5") {
		t.Errorf("replayed epoch: %q", l)
	}
	if l := headerLine(fullState(), 20); len([]rune(l)) > 20 {
		t.Errorf("not clipped: %q", l)
	}
}

func TestHeaderHeldBadge(t *testing.T) {
	s := fullState()
	s.Review = ip(3)
	if l := headerLine(s, 400); !strings.Contains(l, "HELD 3") {
		t.Errorf("no badge: %q", l)
	}
	if l := headerLine(fullState(), 400); strings.Contains(l, "HELD") {
		t.Errorf("badge with zero holds: %q", l)
	}
}

func TestHeaderStreamStates(t *testing.T) {
	o := &overlays{}
	o.stream(sseEvent{Event: "node.updated", ID: "7@abc"})
	if l := headerLine(o.snapshot(), 400); !strings.Contains(l, "live seq 7") {
		t.Errorf("live: %q", l)
	}
	o.link("reconnecting")
	if l := headerLine(o.snapshot(), 400); !strings.Contains(l, "reconnecting") || strings.Contains(l, "live") {
		t.Errorf("reconnecting: %q", l)
	}
	o.stream(sseEvent{Event: "reset"})
	if l := headerLine(o.snapshot(), 400); !strings.Contains(l, "reset") {
		t.Errorf("reset: %q", l)
	}
}

func TestHeaderDegradesOnFetchError(t *testing.T) {
	// Only the summary and epochs routes exist; review and recruiting 404.
	f := newFakeAPI(t, map[string]any{
		"/graphs/g1/summary": map[string]any{"by_work_state": map[string]any{"claimed": 2}},
		"/graphs/g1/epochs":  map[string]any{"items": []any{map[string]any{"epoch": 1, "current": true}}, "total": 1},
	})
	t.Setenv("GRAPH_API_KEY", "k")
	t.Setenv("GRAPH_AUTH_HELPER", "")
	c := newClient(f.URL, apiKey("k"))
	o := &overlays{}
	o.refresh(context.Background(), c, "g1")
	st := o.snapshot()
	if st.Counts["claimed"] != 2 || st.Review != nil || st.Recruiting != nil {
		t.Fatalf("state %+v", st)
	}
	l := headerLine(st, 400)
	if !strings.Contains(l, "2 claimed") || strings.Contains(l, "HELD") || strings.Contains(l, "recruiting") {
		t.Errorf("line %q", l)
	}
	f.onlyReads(t)
}

func TestHeaderNoOverlaysFlag(t *testing.T) {
	// A view without overlays keeps today's plain header: no overlay line.
	v := newView()
	v.title = "slug"
	if v.ov != nil {
		t.Fatal("overlays default on in a bare view")
	}
	plain := v.frame(120, 30, 0.1)
	v.ov = &overlays{}
	v.ov.set(func(s *headerState) { s.Review = ip(2) })
	if with := v.frame(120, 30, 0.1); with == plain || !strings.Contains(with, "HELD 2") {
		t.Error("overlay line not drawn")
	}
	if strings.Contains(plain, "HELD") {
		t.Error("plain header has overlay")
	}
}
