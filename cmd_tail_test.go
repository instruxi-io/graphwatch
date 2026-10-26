package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const tailFrames = `: keepalive
id: 1@1
event: ready
data: {}

id: 2@1
event: node.status_changed
data: {"seq":2,"verb":"node.status_changed","resource_type":"node","resource_id":"n1","actor_account_id":"acct","metadata":{"from":"pending","to":"running"},"created_at":"2026-01-02T03:04:05Z"}

id: 3@1
event: node.created
data: {"seq":3,"verb":"node.created","resource_type":"node","resource_id":"zzz","actor_account_id":"acct","metadata":{},"created_at":"2026-01-02T03:04:06Z"}

id: 4@1
event: closed
data: {}

`

// tailAPI serves nodes and an SSE body, recording Last-Event-ID headers.
func tailAPI(t *testing.T, frames string) (*fakeAPI, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var ids []string
	f := &fakeAPI{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/stream") {
			mu.Lock()
			ids = append(ids, r.Header.Get("Last-Event-ID"))
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(frames))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":[{"id":"n1","key":"node-one","status":"running"}]}`))
	}))
	t.Cleanup(f.Close)
	return f, &ids
}

func TestTailPrintsEvents(t *testing.T) {
	f, _ := tailAPI(t, tailFrames)
	out, _, code := runCLI(t, f, "tail", "g1")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %q", out)
	}
	if !strings.Contains(lines[0], "node.status_changed node-one pending->running acct") {
		t.Errorf("line 0: %q", lines[0])
	}
	if !strings.Contains(lines[1], "node.created zzz acct") {
		t.Errorf("unknown id should print raw: %q", lines[1])
	}
	f.onlyReads(t)
}

func TestTailNDJSON(t *testing.T) {
	f, _ := tailAPI(t, tailFrames)
	out, _, _ := runCLI(t, f, "tail", "--json", "g1")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `{"seq":2`) {
		t.Fatalf("ndjson: %q", out)
	}
}

func TestTailResumesFromCursor(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	f, ids := tailAPI(t, tailFrames)
	runCLI(t, f, "tail", "--cursor", "1@1", "g1")
	if len(*ids) == 0 || (*ids)[0] != "1@1" {
		t.Fatalf("Last-Event-ID not sent: %v", *ids)
	}
	// saved cursor is picked up by the next run
	runCLI(t, f, "tail", "--save-cursor", "g1")
	runCLI(t, f, "tail", "--save-cursor", "g1")
	if got := (*ids)[len(*ids)-1]; got != "4@1" {
		t.Fatalf("second run resumed from %q, want 4@1", got)
	}
}

func TestTailSavesCursorMode0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	f, _ := tailAPI(t, tailFrames)
	runCLI(t, f, "tail", "--save-cursor", "g1")
	st, err := os.Stat(filepath.Join(dir, "graphwatch", "g1.cursor"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}

func TestTailResetNotice(t *testing.T) {
	f, _ := tailAPI(t, "id: 9@2\nevent: reset\ndata: {}\n\nid: 10@2\nevent: closed\ndata: {}\n\n")
	out, _, code := runCLI(t, f, "tail", "g1")
	if code != 0 || !strings.Contains(out, "RESET: cursor too old, replaying from live") {
		t.Fatalf("code %d out %q", code, out)
	}
}
