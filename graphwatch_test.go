package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A diamond plus a tail: design -> {build, docs} -> test -> ship.
func testGraph() ([]apiNode, []apiEdge) {
	ns := []apiNode{{ID: "d", Key: "design", Status: "done"}, {ID: "b", Key: "build", Status: "running"},
		{ID: "o", Key: "docs", Status: "pending"}, {ID: "t", Key: "test", Status: "pending"}, {ID: "s", Key: "ship", Status: "pending"}}
	es := []apiEdge{{"b", "d", "requires"}, {"o", "d", "requires"}, {"t", "b", "requires"}, {"t", "o", "requires"}, {"s", "t", "requires"},
		{"s", "d", "links"}} // not the dependency type: ignored
	return ns, es
}

func TestLayersPutPrerequisitesAbove(t *testing.T) {
	v := newView()
	v.apply(testGraph())
	d := layers(v.nodes)
	want := map[string]int{"d": 0, "b": 1, "o": 1, "t": 2, "s": 3}
	for id, w := range want {
		if d[id] != w {
			t.Errorf("depth[%s] = %d, want %d", id, d[id], w)
		}
	}
}

func TestLayersSurviveACycle(t *testing.T) {
	nodes := map[string]*gnode{"a": {id: "a", prereqs: []string{"b"}}, "b": {id: "b", prereqs: []string{"a"}}}
	if d := layers(nodes); len(d) != 2 {
		t.Fatalf("got %v", d)
	}
}

// Two parents crossed over two children: the barycenter sweep uncrosses them.
func TestOrderUncrossesEdges(t *testing.T) {
	nodes := map[string]*gnode{
		"L": {id: "L", created: 1, children: []string{"r2"}}, "R": {id: "R", created: 2, children: []string{"r1"}},
		"r1": {id: "r1", created: 3, prereqs: []string{"R"}}, "r2": {id: "r2", created: 4, prereqs: []string{"L"}},
	}
	rows := order(nodes, layers(nodes), map[string]float64{})
	if strings.Join(rows[0], ",") != "L,R" || strings.Join(rows[1], ",") != "r2,r1" {
		t.Fatalf("rows = %v", rows)
	}
}

func TestPlaceFansOutTopToBottom(t *testing.T) {
	p := place([][]string{{"a"}, {"b", "c"}}, 80, 4, 30, 0)
	if !(p.y["a"] < p.y["b"]) || p.y["b"] != p.y["c"] || !(p.x["b"] < p.x["c"]) {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlaceScrollsToTheBusyLayer(t *testing.T) {
	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{string(rune('a' + i%26))}
	}
	p := place(rows, 80, 4, 20, 25)
	if y := p.y[rows[25][0]]; y < 4 || y > 20 {
		t.Fatalf("focus layer drawn at row %v, outside the viewport", y)
	}
}

func TestTransitionsEmitParticlesInTheDirectionOfWork(t *testing.T) {
	v := newView()
	ns, es := testGraph()
	v.apply(ns, es)
	ns[3].Status = "running" // test starts: pulses flow in from build and docs
	v.apply(ns, es)
	in := 0
	for _, p := range v.parts {
		if p.to == "t" {
			in++
		}
	}
	if in != 2 {
		t.Fatalf("want 2 particles into test, got %d (%+v)", in, v.parts)
	}
	ns[1].Status = "done" // build finishes: a pulse flows out to test
	v.apply(ns, es)
	found := false
	for _, p := range v.parts {
		if p.from == "b" && p.to == "t" && p.heat > 0.9 {
			found = true
		}
	}
	if !found {
		t.Fatal("no particle out of a finished node")
	}
}

func TestHeatCoolsButRunningNodesKeepPulsing(t *testing.T) {
	v := newView()
	v.apply(testGraph())
	for i := 0; i < 200; i++ {
		v.frame(100, 40, 0.05)
	}
	if h := v.vis["d"].heat; h > 0.05 {
		t.Errorf("an idle done node is still hot after 10s: %v", h)
	}
	if h := v.vis["b"].heat; h < 0.2 {
		t.Errorf("a running node went cold: %v", h)
	}
}

func TestCompletionIsUndoneByNewWork(t *testing.T) {
	v := newView()
	ns := []apiNode{{ID: "a", Key: "a", Status: "done"}}
	v.apply(ns, nil)
	v.event(sseEvent{Event: "graph.completed", Data: []byte(`{"seq":9,"verb":"graph.completed","metadata":{}}`)})
	if _, ok := v.completed(); !ok {
		t.Fatal("graph.completed not recorded")
	}
	v.apply(append(ns, apiNode{ID: "b", Key: "b", Status: "pending"}), nil)
	if _, ok := v.completed(); ok {
		t.Fatal("a plan that grew after completing still reads complete")
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func TestFrameDrawsEveryNodeAndItsLabel(t *testing.T) {
	v := newView()
	v.title = "demo"
	v.apply(testGraph())
	var out string
	for i := 0; i < 60; i++ {
		out = v.frame(100, 36, 0.033)
	}
	plain := ansi.ReplaceAllString(out, "")
	for _, s := range []string{"design", "build", "docs", "test", "ship", "●", "◎", "○", "graphwatch", "demo"} {
		if !strings.Contains(plain, s) {
			t.Errorf("frame is missing %q", s)
		}
	}
	if !strings.ContainsAny(plain, "◐◓◑◒") {
		t.Error("the running node has no spinner")
	}
}

func TestParseSSE(t *testing.T) {
	var got []sseEvent
	body := "event: ready\nid: 1@1:1:\ndata: {\"listener\":\"live\"}\n\n: keepalive\n\nevent: node.created\nid: 2@1:1:\ndata: {\"seq\":2}\r\n\r\n"
	if err := parseSSE(strings.NewReader(body), func(e sseEvent) { got = append(got, e) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Event != "ready" || got[1].Event != ":" || got[2].ID != "2@1:1:" || string(got[2].Data) != `{"seq":2}` {
		t.Fatalf("got %+v", got)
	}
}

func TestThermalRampIsMonotoneInBrightness(t *testing.T) {
	prev := -1.0
	for i := 0; i <= 20; i++ {
		c := thermal(float64(i) / 20)
		l := 0.2126*float64(c.r) + 0.7152*float64(c.g) + 0.0722*float64(c.b)
		if l < prev {
			t.Fatalf("brightness drops at %d: %v < %v", i, l, prev)
		}
		prev = l
	}
	_ = time.Second
}

func TestRunFinishedIsNamedFromItsStart(t *testing.T) {
	v := newView()
	v.apply([]apiNode{{ID: "n1", Key: "compile", Status: "running"}}, nil)
	v.event(sseEvent{Event: "run.started", Data: []byte(`{"seq":1,"verb":"run.started","resource_id":"r1","metadata":{"node":"compile"}}`)})
	v.event(sseEvent{Event: "run.finished", Data: []byte(`{"seq":2,"verb":"run.finished","resource_id":"r1","metadata":{"status":"succeeded"}}`)})
	v.event(sseEvent{Event: "run.judged", Data: []byte(`{"seq":3,"verb":"run.judged","resource_id":"r1","metadata":{"state":"skipped","node_id":"n1"}}`)})
	last := v.ticker[len(v.ticker)-1].text
	if last != "run.finished  compile  succeeded" {
		t.Fatalf("ticker = %q", last)
	}
}

func TestRetryStopsOnAClientErrorAndRetriesAServerError(t *testing.T) {
	ctx := context.Background()
	n := 0
	if err := retry(ctx, func() error { n++; return &apiError{status: 409, msg: "conflict"} }); err == nil || n != 1 {
		t.Fatalf("a 4xx must not be retried: calls=%d err=%v", n, err)
	}
	n = 0
	if err := retry(ctx, func() error {
		n++
		if n < 3 {
			return &apiError{status: 503, msg: "unavailable"}
		}
		return nil
	}); err != nil || n != 3 {
		t.Fatalf("a 5xx must be retried until it succeeds: calls=%d err=%v", n, err)
	}
}
