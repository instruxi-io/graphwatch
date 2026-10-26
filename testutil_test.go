package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAPI is an httptest server for command tests. Routes map a GET path
// (under /api/v1/graph, e.g. "/graphs") to the `data` payload it serves.
type fakeAPI struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []string
	// posts answers a POST path with a status and body; bodies records what was sent.
	posts  map[string]fakeReply
	bodies []string
	// afterPost, when set, runs after a POST is recorded and before it is
	// answered, so a test can change what the next read returns.
	afterPost func()
}

// setRoute replaces what a GET path returns, safely while requests are served.
func (f *fakeAPI) setRoute(routes map[string]any, path string, data any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	routes[path] = data
}

type fakeReply struct {
	status int
	body   map[string]any
}

// newFakeAPI serves {"success":true,"data":...} envelopes and records every
// request as "METHOD /path?query" so a test can assert a command only reads.
// Unknown paths and non-GET methods answer an error envelope.
func newFakeAPI(t *testing.T, routes map[string]any) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := r.Method + " " + r.URL.Path
		if r.URL.RawQuery != "" {
			rec += "?" + r.URL.RawQuery
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, rec)
		reply, isPost := f.posts[strings.TrimPrefix(r.URL.Path, "/api/v1/graph")]
		if r.Method == http.MethodPost {
			var b bytes.Buffer
			b.ReadFrom(r.Body)
			f.bodies = append(f.bodies, b.String())
		}
		hook := f.afterPost
		f.mu.Unlock()
		if r.Method == http.MethodPost && hook != nil {
			hook()
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && isPost {
			w.WriteHeader(reply.status)
			json.NewEncoder(w).Encode(reply.body)
			return
		}
		f.mu.Lock()
		data, ok := routes[strings.TrimPrefix(r.URL.Path, "/api/v1/graph")]
		f.mu.Unlock()
		if r.Method != http.MethodGet || !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"success": false,
				"error": map[string]any{"code": "not_found", "message": "no such route"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	t.Cleanup(f.Close)
	return f
}

// requests returns the recorded requests.
func (f *fakeAPI) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reqs...)
}

// onlyReads fails the test if any recorded request was not a GET.
func (f *fakeAPI) onlyReads(t *testing.T) {
	t.Helper()
	for _, r := range f.requests() {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("a read-only command sent %s", r)
		}
	}
}

// runCLI runs `graphwatch <args>` against the fake with a test API key and
// returns what it wrote and its exit code.
func runCLI(t *testing.T, f *fakeAPI, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("GRAPH_API_KEY", "test-key")
	t.Setenv("GRAPH_AUTH_HELPER", "")
	var out, errb bytes.Buffer
	if f != nil {
		args = append(args[:1:1], append([]string{"--base", f.URL}, args[1:]...)...)
	}
	code, handled := dispatch(args, &out, &errb)
	if !handled {
		t.Fatalf("%v was not dispatched", args)
	}
	return out.String(), errb.String(), code
}
