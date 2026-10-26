package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// queryServer answers /nodes/query and /runs/query from a handler and records
// each request.
func queryServer(t *testing.T, h func(q url.Values) string) (*fakeAPI, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var reqs []string
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqs = append(reqs, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":` + h(r.URL.Query()) + `}`))
	}))
	t.Cleanup(f.Close)
	return f, &reqs
}

func TestQueryNodesWhereFlag(t *testing.T) {
	var got url.Values
	f, _ := queryServer(t, func(q url.Values) string {
		got = q
		return `{"items":[{"graph_id":"11111111-2222","key":"build","status":"failed","work_state":"finished","title":"Build it"}],"total":1}`
	})
	out, errs, code := runCLI(t, f, "query", "nodes", "--status", "failed", "--where", "data.tier:eq:deep", "--where", "data.n:gte:3", "--where", "data.owner:in:a,b", "--where", "data.x:exists")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	want := `[{"op":"eq","path":"data.tier","value":"deep"},{"op":"gte","path":"data.n","value":3},{"op":"in","path":"data.owner","value":["a","b"]},{"op":"exists","path":"data.x"}]`
	if got.Get("where") != want {
		t.Errorf("where = %s\nwant %s", got.Get("where"), want)
	}
	if got.Get("status") != "failed" || got.Get("limit") != "200" {
		t.Errorf("params = %v", got)
	}
	for _, h := range []string{"GRAPH", "KEY", "STATUS", "WORK_STATE", "TITLE", "build", "Build it"} {
		if !strings.Contains(out, h) {
			t.Errorf("output missing %q:\n%s", h, out)
		}
	}
}

func TestQueryFollowsNextCursor(t *testing.T) {
	f, reqs := queryServer(t, func(q url.Values) string {
		switch q.Get("after") {
		case "":
			return `{"items":[{"key":"a"},{"key":"b"}],"next":"CUR1"}`
		case "CUR1":
			return `{"items":[{"key":"c"}]}`
		}
		return `{"items":[]}`
	})
	out, _, code := runCLI(t, f, "query", "nodes", "--all")
	if code != 0 {
		t.Fatal(code)
	}
	if len(*reqs) != 2 || !strings.Contains((*reqs)[1], "after=CUR1") {
		t.Errorf("requests = %v", *reqs)
	}
	for _, k := range []string{"a", "b", "c"} {
		if !strings.Contains(out, k) {
			t.Errorf("missing %s", k)
		}
	}
	// Without --all there is one request.
	f2, reqs2 := queryServer(t, func(q url.Values) string { return `{"items":[{"key":"a"}],"next":"X"}` })
	runCLI(t, f2, "query", "nodes")
	if len(*reqs2) != 1 {
		t.Errorf("no --all made %d requests", len(*reqs2))
	}
	// --limit bounds --all.
	f3, reqs3 := queryServer(t, func(q url.Values) string { return `{"items":[{"key":"a"},{"key":"b"}],"next":"X"}` })
	out3, _, _ := runCLI(t, f3, "query", "nodes", "--all", "--limit", "3")
	if len(*reqs3) != 2 || len(strings.Split(strings.TrimSpace(out3), "\n")) != 4 {
		t.Errorf("limit: %v\n%s", *reqs3, out3)
	}
}

func TestQueryGroupBy(t *testing.T) {
	f, _ := queryServer(t, func(q url.Values) string {
		return `{"groups":[{"key":{"status":"failed"},"count":4,"aggs":{"avg:run_duration":41.2}},{"key":{"status":"done"},"count":9,"aggs":{"avg:run_duration":null}}],"total_groups":2}`
	})
	out, _, code := runCLI(t, f, "query", "nodes", "--group-by", "status", "--agg", "avg:run_duration")
	if code != 0 {
		t.Fatal(code)
	}
	for _, s := range []string{"KEY", "COUNT", "avg:run_duration", "status=failed", "41.2", "status=done"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q:\n%s", s, out)
		}
	}
}

func TestQueryRunsTable(t *testing.T) {
	f, reqs := queryServer(t, func(q url.Values) string {
		return `{"items":[{"attempt":2,"status":"succeeded","runner":"worker-1","node":{"id":"n1","key":"deploy","type":"task"}}]}`
	})
	out, _, code := runCLI(t, f, "query", "runs", "--runner", "worker-1")
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains((*reqs)[0], "/runs/query") || !strings.Contains((*reqs)[0], "runner=worker-1") {
		t.Errorf("request %v", *reqs)
	}
	for _, s := range []string{"NODE", "ATTEMPT", "STATUS", "RUNNER", "deploy", "succeeded", "worker-1"} {
		if !strings.Contains(out, s) {
			t.Errorf("missing %q:\n%s", s, out)
		}
	}
}

func TestQueryReadOnly(t *testing.T) {
	f, _ := queryServer(t, func(q url.Values) string { return `{"items":[]}` })
	runCLI(t, f, "query", "nodes", "--all")
	runCLI(t, f, "query", "runs")
	if len(f.requests()) == 0 {
		t.Fatal("no requests")
	}
	f.onlyReads(t)
}
