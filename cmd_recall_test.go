package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func recallRoutes() map[string]any {
	return map[string]any{
		"/graphs/g1/recall": []any{
			map[string]any{"kind": "decision", "node_key": "build", "created_at": "2026-10-01T10:00:00Z",
				"run_id": "run-1", "body": "use sqlite\nbecause it is embedded"},
			map[string]any{"kind": "note", "node_key": "test", "created_at": "2026-10-02T10:00:00Z",
				"run_id": "run-2", "body": "flaky on CI"},
		},
		"/graphs/g1/context": map[string]any{
			"sha256": "abc123def", "size": 2048, "built_at": "2026-10-03T09:00:00Z", "recall_enabled": true},
	}
}

func TestRecallPrintsHits(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	out, errOut, code := runCLI(t, f, "recall", "--q", "sqlite", "--node", "build", "--limit", "1", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "decision") || !strings.Contains(out, "build") ||
		!strings.Contains(out, "run-1") || !strings.Contains(out, "use sqlite") {
		t.Errorf("missing hit fields: %s", out)
	}
	if strings.Contains(out, "because it is embedded") {
		t.Errorf("printed more than the first line: %s", out)
	}
	if strings.Contains(out, "flaky on CI") {
		t.Errorf("--limit 1 printed a second hit: %s", out)
	}
	reqs := strings.Join(f.requests(), "\n")
	for _, want := range []string{"q=sqlite", "node=build", "limit=1"} {
		if !strings.Contains(reqs, want) {
			t.Errorf("request lacks %s: %s", want, reqs)
		}
	}
	f.onlyReads(t)
}

func TestContextPrintsPack(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	out, errOut, code := runCLI(t, f, "context", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"abc123def", "2048", "2026-10-03T09:00:00Z", "true"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
	f.onlyReads(t)
}

func TestRecallUnknownGraphExitsOne(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	for _, cmd := range []string{"recall", "context"} {
		_, errOut, code := runCLI(t, f, cmd, "nope")
		if code != 1 || !strings.Contains(errOut, "no graph nope") {
			t.Errorf("%s: exit %d, stderr %q", cmd, code, errOut)
		}
	}
}

func withRecallNow(t *testing.T, now string) {
	t.Helper()
	fixed, err := time.Parse(time.RFC3339, now)
	if err != nil {
		t.Fatal(err)
	}
	old := recallNow
	recallNow = func() time.Time { return fixed }
	t.Cleanup(func() { recallNow = old })
}

func TestRecallSinceDropsOldHits(t *testing.T) {
	withRecallNow(t, "2026-10-02T12:00:00Z")
	f := newFakeAPI(t, recallRoutes())
	out, errOut, code := runCLI(t, f, "recall", "--since", "3h", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "flaky on CI") {
		t.Errorf("dropped the recent hit: %s", out)
	}
	if strings.Contains(out, "use sqlite") {
		t.Errorf("kept the hit older than the window: %s", out)
	}
}

func TestRecallSinceAcceptsDays(t *testing.T) {
	withRecallNow(t, "2026-10-04T11:00:00Z")
	f := newFakeAPI(t, recallRoutes())
	out, errOut, code := runCLI(t, f, "recall", "--since", "3d", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(out, "use sqlite") || !strings.Contains(out, "flaky on CI") {
		t.Errorf("3d window kept the wrong hits: %s", out)
	}
}

func TestRecallSinceBadDurationExitsTwo(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	_, errOut, code := runCLI(t, f, "recall", "--since", "soon", "g1")
	if code != 2 || !strings.Contains(errOut, "--since: bad duration soon") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestRecallSortTimeNewestFirst(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	out, errOut, code := runCLI(t, f, "recall", "--sort", "time", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	newer, older := strings.Index(out, "flaky on CI"), strings.Index(out, "use sqlite")
	if newer < 0 || older < 0 || newer > older {
		t.Errorf("not newest first: %s", out)
	}
	out, _, _ = runCLI(t, f, "recall", "g1")
	if strings.Index(out, "use sqlite") > strings.Index(out, "flaky on CI") {
		t.Errorf("default order is not the server's: %s", out)
	}
}

func TestRecallLimitOver50ExitsTwoWithoutRequest(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	_, errOut, code := runCLI(t, f, "recall", "--limit", "51", "g1")
	if code != 2 || !strings.Contains(errOut, "--limit: at most 50") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("server saw %d requests: %v", n, f.requests())
	}
}

func TestRecallSortBadValueExitsTwo(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	_, errOut, code := runCLI(t, f, "recall", "--sort", "size", "g1")
	if code != 2 || !strings.Contains(errOut, "--sort must be rank or time") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestRecallEmptyPrintsNoFindings(t *testing.T) {
	f := newFakeAPI(t, map[string]any{"/graphs/g1/recall": []any{}})
	out, errOut, code := runCLI(t, f, "recall", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.TrimSpace(out) != "no findings" {
		t.Errorf("empty result printed %q", out)
	}
}

func TestRecallSinceJSONIsValid(t *testing.T) {
	withRecallNow(t, "2026-10-02T12:00:00Z")
	f := newFakeAPI(t, recallRoutes())
	out, errOut, code := runCLI(t, f, "recall", "--since", "3h", "--json", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("--since with --json is not valid JSON: %v\n%s", err, out)
	}
}

func TestRecallSortTimeIsStable(t *testing.T) {
	same := "2026-10-02T10:00:00Z"
	f := newFakeAPI(t, map[string]any{"/graphs/g1/recall": []any{
		map[string]any{"kind": "note", "node_key": "n1", "created_at": same, "body": "first-hit"},
		map[string]any{"kind": "note", "node_key": "n2", "created_at": same, "body": "second-hit"},
		map[string]any{"kind": "note", "node_key": "n3", "created_at": same, "body": "third-hit"},
	}})
	out, errOut, code := runCLI(t, f, "recall", "--sort", "time", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	a, b, c := strings.Index(out, "first-hit"), strings.Index(out, "second-hit"), strings.Index(out, "third-hit")
	if a < 0 || b < 0 || c < 0 || !(a < b && b < c) {
		t.Errorf("equal timestamps were reordered: %s", out)
	}
}

// pagedRecallAPI serves 120 hits at 50 a page, honouring limit and offset.
// Hit i is i minutes old, so the only hit inside a 1h window with ids below
// 60 minutes is deep in the list when the server orders them oldest first.
func pagedRecallAPI(t *testing.T) *fakeAPI {
	t.Helper()
	base, _ := time.Parse(time.RFC3339, "2026-10-02T12:00:00Z")
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var page []any
		for i := off; i < 120 && len(page) < limit; i++ {
			// Hits 0..99 are two days old; 100..119 are recent.
			age := 48 * time.Hour
			if i >= 100 {
				age = time.Duration(i-99) * time.Minute
			}
			page = append(page, map[string]any{"kind": "note", "node_key": "n" + strconv.Itoa(i),
				"created_at": base.Add(-age).Format(time.RFC3339), "body": "hit-" + strconv.Itoa(i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": page})
	}))
	t.Cleanup(f.Close)
	return f
}

func TestRecallSinceFetchesBeyondFirstPage(t *testing.T) {
	withRecallNow(t, "2026-10-02T12:00:00Z")
	f := pagedRecallAPI(t)
	out, errOut, code := runCLI(t, f, "recall", "--since", "1h", "--limit", "5", "g1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(out, "no findings") || !strings.Contains(out, "hit-100") {
		t.Errorf("did not find the recent hit below the first page: %s", out)
	}
	if strings.Contains(out, "hit-5 ") || strings.Count(out, "hit-") != 5 {
		t.Errorf("want exactly 5 recent hits: %s", out)
	}
}

func TestRecallZeroSinceExitsTwo(t *testing.T) {
	f := newFakeAPI(t, recallRoutes())
	for _, v := range []string{"0s", "0d", "0"} {
		_, errOut, code := runCLI(t, f, "recall", "--since", v, "g1")
		if code != 2 || !strings.Contains(errOut, "--since") {
			t.Errorf("--since %s: exit %d, stderr %q", v, code, errOut)
		}
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("server saw %d requests", n)
	}
}

func TestPageContinuesWithoutMetaTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var rows []map[string]any
		for i := off; i < 450 && len(rows) < 200; i++ {
			rows = append(rows, map[string]any{"id": strconv.Itoa(i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": rows})
	}))
	defer srv.Close()
	c := newClient(srv.URL, apiKey("k"))
	got, err := page[map[string]any](context.Background(), c, "/graphs/g/nodes")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 450 {
		t.Errorf("read %d rows, want 450", len(got))
	}
}
