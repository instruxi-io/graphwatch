package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeHelper writes a shell script that prints a Bearer header whose token is
// the number of times it has run, so a test can tell a cached answer from a
// fresh one.
func fakeHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	count := filepath.Join(dir, "n")
	script := filepath.Join(dir, "helper.sh")
	body := "#!/bin/sh\nn=$(cat " + count + " 2>/dev/null || echo 0); n=$((n+1)); echo $n > " + count +
		"\nprintf '{\"Authorization\":\"Bearer tok-%s\"}' \"$n\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return "sh " + script
}

func TestHelperHeadersAreCachedUntilAskedFresh(t *testing.T) {
	h := &helper{cmd: fakeHelper(t)}
	ctx := context.Background()
	a, err := h.headers(ctx, false)
	if err != nil || a["Authorization"] != "Bearer tok-1" {
		t.Fatalf("first = %v, %v", a, err)
	}
	b, _ := h.headers(ctx, false)
	if b["Authorization"] != "Bearer tok-1" {
		t.Fatalf("cached = %v, want tok-1", b)
	}
	c, _ := h.headers(ctx, true)
	if c["Authorization"] != "Bearer tok-2" {
		t.Fatalf("fresh = %v, want tok-2", c)
	}
}

func TestHelperThatPrintsNothingIsSignedOut(t *testing.T) {
	h := &helper{cmd: "printf '{}'"}
	if _, err := h.headers(context.Background(), false); !errors.Is(err, errSignedOut) {
		t.Fatalf("err = %v, want errSignedOut", err)
	}
	h = &helper{cmd: "echo not-json"}
	if _, err := h.headers(context.Background(), false); err == nil || strings.Contains(err.Error(), "not-json") {
		t.Fatalf("err = %v: want a failure that does not echo stdout (it may hold a token)", err)
	}
}

func TestApiKeyCredentialSendsXAPIKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-API-Key") + "|" + r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL, apiKey("k-123"))
	if err := c.do(context.Background(), http.MethodGet, "/graphs", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got != "k-123|" {
		t.Fatalf("headers = %q, want the key and no Authorization", got)
	}
}

// A 401 re-runs the helper (which refreshes an expired OAuth token) and resends
// once; the second answer is used, and a second 401 is final.
func TestA401RefreshesTheHelperAndRetriesOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer tok-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := newClient(srv.URL, &helper{cmd: fakeHelper(t)})
	var out map[string]bool
	if err := c.do(context.Background(), http.MethodPost, "/x", map[string]int{"a": 1}, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if !out["ok"] || calls.Load() != 2 {
		t.Fatalf("out=%v calls=%d, want ok after exactly one retry", out, calls.Load())
	}

	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer always.Close()
	calls.Store(0)
	c = newClient(always.URL, &helper{cmd: fakeHelper(t)})
	err := c.do(context.Background(), http.MethodGet, "/x", nil, nil)
	var ae *apiError
	if !errors.As(err, &ae) || ae.status != http.StatusUnauthorized || calls.Load() != 2 {
		t.Fatalf("err=%v calls=%d, want a final 401 after one retry", err, calls.Load())
	}
}
