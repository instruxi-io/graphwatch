package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAS is an authorization server with single-use rotating refresh tokens,
// like Enforcer's: a refresh token works once, and spending a dead one is 400
// invalid_grant.
type fakeAS struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	n         int
	clients   map[string]bool
	codes     map[string]string // code -> challenge
	live      map[string]bool   // refresh tokens that still work
	refreshes atomic.Int32
	revoked   []string
	registers atomic.Int32
	lastAuth  url.Values
}

func newFakeAS(t *testing.T) *fakeAS {
	a := &fakeAS{t: t, clients: map[string]bool{}, codes: map[string]string{}, live: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		b := a.srv.URL
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": b, "authorization_endpoint": b + "/authorize", "token_endpoint": b + "/token",
			"registration_endpoint": b + "/register", "revocation_endpoint": b + "/revoke",
			"scopes_supported": []string{"enforcer:read", "enforcer:graph-review.write", "enforcer:graph-validation.write", "offline_access"},
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		a.registers.Add(1)
		a.mu.Lock()
		a.n++
		id := fmt.Sprintf("client-%d", a.n)
		a.clients[id] = true
		a.mu.Unlock()
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"client_id": id, "scope": "enforcer:read enforcer:graph-review.write enforcer:graph-validation.write"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a.mu.Lock()
		a.lastAuth = q
		a.n++
		code := fmt.Sprintf("code-%d", a.n)
		a.codes[code] = q.Get("code_challenge")
		a.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.clients[r.Form.Get("client_id")] {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			ch, ok := a.codes[r.Form.Get("code")]
			delete(a.codes, r.Form.Get("code"))
			if !ok || ch == "" || r.Form.Get("code_verifier") == "" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
		case "refresh_token":
			rt := r.Form.Get("refresh_token")
			if !a.live[rt] {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			delete(a.live, rt)
			a.refreshes.Add(1)
		}
		a.n++
		rt := fmt.Sprintf("rt-%d", a.n)
		a.live[rt] = true
		json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("at-%d", a.n), "refresh_token": rt,
			"expires_in": 3600, "refresh_token_expires_in": 86400, "scope": "enforcer:read enforcer:graph-review.write"})
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		a.mu.Lock()
		a.revoked = append(a.revoked, r.Form.Get("token"))
		delete(a.live, r.Form.Get("token"))
		a.mu.Unlock()
	})
	mux.HandleFunc("/api/v1/graph/status", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at-") {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"data":{"email":"pat@example.com","tenant":"acme","role":"admin"}}`)
	})
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

// browser follows the authorization URL to the loopback redirect, as a
// browser would.
func browser(u string) error {
	resp, err := http.Get(u)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func doLogin(t *testing.T, a *fakeAS, dir string, mod func(*loginOptions)) *loginResult {
	t.Helper()
	o := loginOptions{base: a.srv.URL, scopes: defaultLoginScopes, timeout: 5 * time.Second, out: io.Discard, openURL: browser}
	if mod != nil {
		mod(&o)
	}
	res, err := browserLogin(context.Background(), dir, o)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return res
}

func TestLoginStoresSessionPrivately(t *testing.T) {
	a := newFakeAS(t)
	dir := filepath.Join(t.TempDir(), "gw")
	res := doLogin(t, a, dir, nil)
	if res.session.AccessToken == "" || res.session.RefreshToken == "" {
		t.Fatal("no tokens saved")
	}
	if got := a.lastAuth.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q", got)
	}
	if got := a.lastAuth.Get("resource"); got != a.srv.URL {
		t.Fatalf("resource = %q, want the API base", got)
	}
	if got := a.lastAuth.Get("scope"); !strings.Contains(got, "enforcer:graph-review.write") {
		t.Fatalf("scope = %q", got)
	}
	st, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.json mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", d.Mode().Perm())
	}
	if len(res.notGranted) != 1 || res.notGranted[0] != "enforcer:graph-validation.write" {
		t.Fatalf("notGranted = %v", res.notGranted)
	}
}

func TestLoginReusesClientAndRevokesReplacedSession(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	first := doLogin(t, a, dir, nil)
	doLogin(t, a, dir, nil)
	if n := a.registers.Load(); n != 1 {
		t.Fatalf("registered %d clients, want 1 reused", n)
	}
	if len(a.revoked) != 1 || a.revoked[0] != first.session.RefreshToken {
		t.Fatalf("revoked = %v, want the first session's refresh token", a.revoked)
	}
	doLogin(t, a, dir, func(o *loginOptions) { o.newApp = true })
	if n := a.registers.Load(); n != 2 {
		t.Fatalf("--new-app registered %d clients total, want 2", n)
	}
}

func TestLoginRefusesStateMismatchFromPaste(t *testing.T) {
	a := newFakeAS(t)
	_, err := browserLogin(context.Background(), t.TempDir(), loginOptions{
		base: a.srv.URL, scopes: defaultLoginScopes, timeout: 5 * time.Second, out: io.Discard,
		in: strings.NewReader("http://127.0.0.1:1/callback?code=x&state=forged\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("err = %v, want state mismatch", err)
	}
}

// The SSH path: no browser on this machine; the user pastes where theirs landed.
func TestLoginAcceptsPastedRedirect(t *testing.T) {
	a := newFakeAS(t)
	pr, pw := io.Pipe()
	var out bytes.Buffer
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) })
	done := make(chan error, 1)
	go func() {
		_, err := browserLogin(context.Background(), t.TempDir(), loginOptions{
			base: a.srv.URL, scopes: defaultLoginScopes, timeout: 5 * time.Second, out: w, in: pr, noBrowser: true,
		})
		done <- err
	}()
	// Read the printed address, follow it without following the redirect,
	// and paste the Location back, as a person on another machine would.
	var authURL string
	for i := 0; i < 200 && authURL == ""; i++ {
		mu.Lock()
		for _, l := range strings.Split(out.String(), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, a.srv.URL+"/authorize") {
				authURL = l
			}
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	nc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := nc.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	io.WriteString(pw, "not a url\n"+resp.Header.Get("Location")+"\n")
	if err := <-done; err != nil {
		t.Fatalf("pasted login: %v", err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func expireSession(t *testing.T, dir, base string) {
	t.Helper()
	s, _ := readLoginStore(dir)
	s.Sessions[base].ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	if err := writeLoginStore(dir, s); err != nil {
		t.Fatal(err)
	}
}

func TestLoginCredentialRefreshesAndRotates(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	first := doLogin(t, a, dir, nil)
	expireSession(t, dir, a.srv.URL)
	c := &loginCredential{dir: dir, base: a.srv.URL, hc: newOAuthHTTP()}
	h, err := c.headers(context.Background(), false)
	if err != nil || h["Authorization"] == "Bearer "+first.session.AccessToken {
		t.Fatalf("headers = %v, %v; want a refreshed token", h, err)
	}
	s, _ := readLoginStore(dir)
	if s.Sessions[a.srv.URL].RefreshToken == first.session.RefreshToken {
		t.Fatal("rotated refresh token was not saved")
	}
	// Cached now: no second refresh.
	c.headers(context.Background(), false)
	if n := a.refreshes.Load(); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
}

// Two graphwatch processes on one machine must not both spend the single-use
// refresh token; the loser would sign the machine out.
func TestLoginConcurrentRefreshSpendsTokenOnce(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	doLogin(t, a, dir, nil)
	expireSession(t, dir, a.srv.URL)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &loginCredential{dir: dir, base: a.srv.URL, hc: newOAuthHTTP()}
			if _, err := c.headers(context.Background(), false); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("a concurrent refresh failed: %v", err)
	}
	if n := a.refreshes.Load(); n != 1 {
		t.Fatalf("refresh token spent %d times, want 1", n)
	}
}

// After a 401 another process's newer token is used before spending ours.
func TestLoginFreshUsesTokenAnotherProcessSaved(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	doLogin(t, a, dir, nil)
	c := &loginCredential{dir: dir, base: a.srv.URL, hc: newOAuthHTTP()}
	old, _ := c.headers(context.Background(), false)
	doLogin(t, a, dir, nil) // a re-login elsewhere
	h, err := c.headers(context.Background(), true)
	if err != nil || h["Authorization"] == old["Authorization"] {
		t.Fatalf("fresh headers = %v, %v; want the re-login's token", h, err)
	}
	if n := a.refreshes.Load(); n != 0 {
		t.Fatalf("refreshes = %d, want 0 (the saved token was good)", n)
	}
}

func TestLoginRevokedSessionSaysSignInAgain(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	doLogin(t, a, dir, nil)
	a.mu.Lock()
	a.live = map[string]bool{} // revoked server-side
	a.mu.Unlock()
	expireSession(t, dir, a.srv.URL)
	c := &loginCredential{dir: dir, base: a.srv.URL, hc: newOAuthHTTP()}
	if _, err := c.headers(context.Background(), false); err != errLoginExpired {
		t.Fatalf("err = %v, want errLoginExpired", err)
	}
	s, _ := readLoginStore(dir)
	if s.Sessions[a.srv.URL].RefreshToken != "" {
		t.Fatal("dead refresh token kept; every run would spend it again")
	}
	// And the next call fails fast without the network.
	before := a.refreshes.Load()
	if _, err := c.headers(context.Background(), false); err != errLoginExpired {
		t.Fatalf("second err = %v", err)
	}
	if a.refreshes.Load() != before {
		t.Fatal("retried a dead token")
	}
}

func TestLoginOutageKeepsRefreshToken(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	first := doLogin(t, a, dir, nil)
	expireSession(t, dir, a.srv.URL)
	a.srv.Close()
	c := &loginCredential{dir: dir, base: a.srv.URL, hc: newOAuthHTTP()}
	if _, err := c.headers(context.Background(), false); err == nil || err == errLoginExpired {
		t.Fatalf("err = %v, want a network error, not signed out", err)
	}
	s, _ := readLoginStore(dir)
	if s.Sessions[a.srv.URL].RefreshToken != first.session.RefreshToken {
		t.Fatal("an outage dropped the refresh token")
	}
}

func TestSameOriginOrLoopback(t *testing.T) {
	for _, tc := range []struct {
		ep   string
		ok   bool
		name string
	}{
		{"https://api.instruxi.dev/oauth/token", true, "same host"},
		{"https://evil.example/token", false, "other host"},
		{"http://api.instruxi.dev/token", false, "cleartext"},
		{"http://127.0.0.1:8080/token", false, "loopback but not the API host"},
	} {
		err := sameOriginOrLoopback(tc.ep, "https://api.instruxi.dev", "https://api.instruxi.dev")
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	if err := sameOriginOrLoopback("http://127.0.0.1:8080/token", "http://127.0.0.1:8080", ""); err != nil {
		t.Errorf("loopback test server refused: %v", err)
	}
}

func TestResolveOrderPutsLoginAfterEnvBeforePlugin(t *testing.T) {
	a := newFakeAS(t)
	home := t.TempDir()
	cfg := filepath.Join(home, "cfg")
	doLogin(t, a, cfg, nil)
	// A plugin helper is installed too.
	pd := filepath.Join(home, ".claude", "plugins", "cache", "m", "enforcer", "1.0.0", "bin")
	os.MkdirAll(pd, 0o755)
	os.WriteFile(filepath.Join(pd, "enforcer-headers.mjs"), nil, 0o644)

	env := map[string]string{"GRAPHWATCH_CONFIG_DIR": cfg, "GRAPH_BASE_URL": a.srv.URL}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	_, src, _, err := resolveCredential(look, home, authFlags{})
	if err != nil || !strings.HasPrefix(src, "graphwatch login") {
		t.Fatalf("source = %q, %v; want graphwatch login ahead of the plugin", src, err)
	}
	env["GRAPH_API_KEY"] = "k"
	if _, src, _, _ = resolveCredential(look, home, authFlags{}); src != "GRAPH_API_KEY" {
		t.Fatalf("source = %q; an env key must still win", src)
	}
	delete(env, "GRAPH_API_KEY")
	env["GRAPH_BASE_URL"] = "https://other.example"
	if _, src, _, _ = resolveCredential(look, home, authFlags{}); !strings.HasPrefix(src, "plugin") {
		t.Fatalf("source = %q; a sign-in for another API must not be used", src)
	}
}

func TestLogoutRevokesAndForgets(t *testing.T) {
	a := newFakeAS(t)
	dir := t.TempDir()
	res := doLogin(t, a, dir, nil)
	t.Setenv("GRAPHWATCH_CONFIG_DIR", dir)
	t.Setenv("GRAPH_BASE_URL", a.srv.URL)
	var out, errb bytes.Buffer
	if code := runLogout(nil, &out, &errb); code != exitOK {
		t.Fatalf("logout = %d: %s", code, errb.String())
	}
	if len(a.revoked) != 1 || a.revoked[0] != res.session.RefreshToken {
		t.Fatalf("revoked = %v", a.revoked)
	}
	s, _ := readLoginStore(dir)
	if s.Sessions[a.srv.URL] != nil {
		t.Fatal("session kept after logout")
	}
	if s.Clients[a.srv.URL] == nil {
		t.Fatal("client registration dropped; the next login would re-register")
	}
	out.Reset()
	runLogout(nil, &out, &errb)
	if !strings.Contains(out.String(), "was not signed in") {
		t.Fatalf("second logout said %q", out.String())
	}
}

func TestLoginTokenNeverInOutput(t *testing.T) {
	a := newFakeAS(t)
	var out bytes.Buffer
	res := doLogin(t, a, t.TempDir(), func(o *loginOptions) { o.out = &out })
	if strings.Contains(out.String(), res.session.AccessToken) || strings.Contains(out.String(), res.session.RefreshToken) {
		t.Fatal("a token was printed")
	}
}
