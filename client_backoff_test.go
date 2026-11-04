package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// freshCount is a credential that counts how often it is asked for fresh headers.
type freshCount struct{ fresh atomic.Int32 }

func (f *freshCount) headers(_ context.Context, fresh bool) (map[string]string, error) {
	if fresh {
		f.fresh.Add(1)
	}
	return map[string]string{"X-API-Key": "dead"}, nil
}

// A dead credential must not be resent every tick: after a 401 that survives
// the refresh retry, the client refuses locally until the backoff ends, then
// tries once more with fresh headers, and a success clears it.
// (2026-11-04: one graphwatch on a lapsed token sent ~3,600 401s an hour.)
func TestDeadCredentialBacksOff(t *testing.T) {
	var hits atomic.Int32
	var ok atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if ok.Load() {
			w.Write([]byte(`{"data":{}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	cred := &freshCount{}
	c := newClient(srv.URL, cred)
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()
	get := func() error { return c.do(ctx, http.MethodGet, "/graphs/g/nodes", nil, nil) }
	is401 := func(err error) bool {
		var ae *apiError
		return errors.As(err, &ae) && ae.status == http.StatusUnauthorized
	}

	// First call: the request and its one refresh retry both reach the server.
	if err := get(); !is401(err) {
		t.Fatalf("first call: want 401, got %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("first call: want 2 hits (request + refresh retry), got %d", n)
	}

	// Inside the backoff: many ticks, no traffic, still a 401 to the caller.
	for i := 0; i < 20; i++ {
		now = now.Add(time.Second)
		if err := get(); !is401(err) {
			t.Fatalf("held call %d: want 401, got %v", i, err)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("during backoff: want no new hits, got %d total", n)
	}

	// Backoff over (30s): one probe, with fresh headers from the start.
	freshBefore := cred.fresh.Load()
	now = now.Add(authBackoffMin)
	if err := get(); !is401(err) {
		t.Fatalf("probe: want 401, got %v", err)
	}
	if n := hits.Load(); n != 4 {
		t.Fatalf("probe: want 2 more hits, got %d total", n)
	}
	if cred.fresh.Load()-freshBefore != 2 {
		t.Fatalf("probe: both attempts should use fresh headers, got %d fresh reads", cred.fresh.Load()-freshBefore)
	}

	// The wait doubled: 30s later is still held.
	now = now.Add(authBackoffMin)
	_ = get()
	if n := hits.Load(); n != 4 {
		t.Fatalf("second backoff should be 60s: got %d hits", n)
	}

	// The wait is capped.
	for i := 0; i < 10; i++ {
		now = now.Add(authBackoffMax)
		_ = get()
	}
	if c.authWait != authBackoffMax {
		t.Fatalf("backoff not capped: %v", c.authWait)
	}

	// Signed in again: the next probe succeeds and the hold is cleared.
	ok.Store(true)
	now = now.Add(authBackoffMax)
	if err := get(); err != nil {
		t.Fatalf("after re-login: %v", err)
	}
	before := hits.Load()
	if err := get(); err != nil || hits.Load() != before+1 {
		t.Fatalf("after success the client should send normally: err=%v hits=%d->%d", err, before, hits.Load())
	}
}
