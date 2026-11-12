package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// graphwatch login: its own browser sign-in, so graphwatch works without the
// enforcer plugin. OAuth 2.1 authorization code + PKCE with a loopback
// redirect (RFC 8252), a public client registered once per machine and API
// (RFC 7591), and the session kept in graphwatch's own file. It never reads or
// writes the plugin's oauth block: refresh tokens are single-use and rotate, so
// two programs refreshing one session would sign each other out.

func init() {
	register("login", "sign in through the browser (OAuth, no plugin needed); --read-only, --no-browser for SSH", nil)
	register("logout", "sign out: revoke and forget graphwatch's own sign-in for this API", nil)
}

const (
	refreshSkew      = 60 * time.Second
	oauthHTTPTimeout = 15 * time.Second
	lockStaleAfter   = 30 * time.Second
	lockWaitMax      = 10 * time.Second
)

// defaultLoginScopes: read everything, and the two write families graphwatch
// itself uses (decide → validation, resolve/attend/answer → review). Only the
// ones the server offers are asked for.
var defaultLoginScopes = []string{"enforcer:read", "enforcer:graph-review.write", "enforcer:graph-validation.write", "offline_access"}
var readOnlyLoginScopes = []string{"enforcer:read", "offline_access"}

// ---- the store --------------------------------------------------------------

// loginStore is graphwatch's credentials file: one registered client per
// issuer, one session per API base, so dev, UAT and prod can all be signed in.
type loginStore struct {
	Clients  map[string]*oauthClient  `json:"clients,omitempty"`
	Sessions map[string]*loginSession `json:"sessions,omitempty"`
}

type oauthClient struct {
	ClientID string `json:"client_id"`
	Ceiling  string `json:"ceiling,omitempty"` // scopes the server allowed at registration
}

type loginSession struct {
	Issuer             string `json:"issuer"`
	ClientID           string `json:"client_id"`
	TokenEndpoint      string `json:"token_endpoint"`
	RevocationEndpoint string `json:"revocation_endpoint,omitempty"`
	AccessToken        string `json:"access_token"`
	RefreshToken       string `json:"refresh_token,omitempty"`
	ExpiresAt          string `json:"expires_at"`
	RefreshExpiresAt   string `json:"refresh_expires_at,omitempty"`
	Scope              string `json:"scope,omitempty"`
	SignedInAt         string `json:"signed_in_at"`
	Account            string `json:"account,omitempty"` // display only
}

// loginDir is $GRAPHWATCH_CONFIG_DIR, else <user config dir>/graphwatch
// (~/.config/graphwatch on Linux, ~/Library/Application Support on macOS).
func loginDir(get func(string) string, home string) string {
	if d := get("GRAPHWATCH_CONFIG_DIR"); d != "" {
		return d
	}
	if d := get("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "graphwatch")
	}
	if runtime.GOOS == "darwin" && home != "" {
		return filepath.Join(home, "Library", "Application Support", "graphwatch")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "graphwatch")
}

func readLoginStore(dir string) (*loginStore, error) {
	s := &loginStore{}
	b, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", filepath.Join(dir, "credentials.json"), err)
	}
	return s, nil
}

// writeLoginStore writes atomically (temp file, fsync, rename) with 0600 in a
// 0700 directory, so a crash never leaves half a token file and no other user
// can read it.
func writeLoginStore(dir string, s *loginStore) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".credentials-*.json")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "credentials.json"))
}

// acquireLock takes the directory's refresh lock (mkdir is atomic on every
// platform graphwatch builds for). A lock older than lockStaleAfter belongs to
// a process that died mid-refresh and is broken.
func acquireLock(ctx context.Context, dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock := filepath.Join(dir, ".refresh.lock")
	deadline := time.Now().Add(lockWaitMax)
	for {
		if err := os.Mkdir(lock, 0o700); err == nil {
			return func() { os.Remove(lock) }, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, err := os.Stat(lock); err == nil && time.Since(st.ModTime()) > lockStaleAfter {
			os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("another graphwatch is refreshing the sign-in and did not finish")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// ---- the credential ---------------------------------------------------------

// errLoginExpired: the server refused the refresh token (expired, revoked, or
// spent by another copy of the file). Only a new sign-in recovers.
var errLoginExpired = errors.New("graphwatch's sign-in has expired or was revoked — run graphwatch login")

// loginCredential serves a session from the store, refreshing it under the
// lock when it is within refreshSkew of expiry or the server just refused it.
type loginCredential struct {
	dir, base string
	hc        *http.Client
	now       func() time.Time

	mu  sync.Mutex
	tok string
	exp time.Time
}

func (c *loginCredential) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *loginCredential) headers(ctx context.Context, fresh bool) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.tok != "" && c.exp.Sub(c.clock()) > refreshSkew {
		return bearer(c.tok), nil
	}
	refused := ""
	if fresh {
		refused = c.tok
	}
	tok, exp, err := c.refresh(ctx, refused)
	if err != nil {
		c.tok = ""
		return nil, err
	}
	c.tok, c.exp = tok, exp
	return bearer(tok), nil
}

func bearer(t string) map[string]string { return map[string]string{"Authorization": "Bearer " + t} }

// refresh returns a usable access token. refused is the token the server just
// answered 401 to ("" when none): another process may already have replaced
// it, and that replacement is used rather than spending the refresh token.
func (c *loginCredential) refresh(ctx context.Context, refused string) (string, time.Time, error) {
	// Fast path, no lock: the file may already hold a good token.
	if s, _ := c.session(); s != nil {
		if exp, ok := s.usable(c.clock(), refused); ok {
			return s.AccessToken, exp, nil
		}
	}
	release, err := acquireLock(ctx, c.dir)
	if err != nil {
		return "", time.Time{}, err
	}
	defer release()
	// Re-read under the lock: whoever held it may have rotated the pair.
	store, err := readLoginStore(c.dir)
	if err != nil {
		return "", time.Time{}, err
	}
	s := store.Sessions[c.base]
	if s == nil || s.AccessToken == "" {
		return "", time.Time{}, errLoginExpired
	}
	if exp, ok := s.usable(c.clock(), refused); ok {
		return s.AccessToken, exp, nil
	}
	if s.RefreshToken == "" {
		return "", time.Time{}, errLoginExpired
	}
	if err := sameOriginOrLoopback(s.TokenEndpoint, c.base, s.Issuer); err != nil {
		return "", time.Time{}, err
	}
	tok, status, err := postToken(ctx, c.hc, s.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {s.RefreshToken},
		"client_id":     {s.ClientID},
	})
	if err != nil {
		if status >= 400 && status < 500 {
			// A definite refusal: forget the dead pair so every later run says
			// "sign in again" at once instead of retrying a spent token.
			s.AccessToken, s.RefreshToken = "", ""
			_ = writeLoginStore(c.dir, store)
			return "", time.Time{}, errLoginExpired
		}
		// Network or 5xx: the refresh token was not spent. Keep it.
		return "", time.Time{}, fmt.Errorf("could not refresh the sign-in: %w", err)
	}
	now := c.clock()
	s.apply(tok, now)
	if err := writeLoginStore(c.dir, store); err != nil {
		return "", time.Time{}, fmt.Errorf("refreshed, but could not save the new token: %w", err)
	}
	exp, _ := time.Parse(time.RFC3339, s.ExpiresAt)
	return s.AccessToken, exp, nil
}

func (c *loginCredential) session() (*loginSession, error) {
	store, err := readLoginStore(c.dir)
	if err != nil {
		return nil, err
	}
	return store.Sessions[c.base], nil
}

func (s *loginSession) usable(now time.Time, refused string) (time.Time, bool) {
	if s.AccessToken == "" || s.AccessToken == refused {
		return time.Time{}, false
	}
	exp, err := time.Parse(time.RFC3339, s.ExpiresAt)
	if err != nil || exp.Sub(now) <= refreshSkew {
		return time.Time{}, false
	}
	return exp, true
}

func (s *loginSession) apply(t *tokenResponse, now time.Time) {
	s.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		s.RefreshToken = t.RefreshToken
	}
	exp := t.ExpiresIn
	if exp <= 0 {
		exp = 900
	}
	s.ExpiresAt = now.Add(time.Duration(exp) * time.Second).UTC().Format(time.RFC3339)
	if r := t.refreshExpiresIn(); r > 0 {
		s.RefreshExpiresAt = now.Add(time.Duration(r) * time.Second).UTC().Format(time.RFC3339)
	}
	if t.Scope != "" {
		s.Scope = t.Scope
	}
}

// ---- OAuth wire -------------------------------------------------------------

type oauthMeta struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	RevocationEndpoint    string   `json:"revocation_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

type tokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	RefreshExpiresIn      int64  `json:"refresh_expires_in"`
	Scope                 string `json:"scope"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
}

func (t *tokenResponse) refreshExpiresIn() int64 {
	if t.RefreshTokenExpiresIn > 0 {
		return t.RefreshTokenExpiresIn
	}
	return t.RefreshExpiresIn
}

func newOAuthHTTP() *http.Client {
	// The default transport: honours HTTPS_PROXY / NO_PROXY and the system CAs.
	return &http.Client{Timeout: oauthHTTPTimeout}
}

func discover(ctx context.Context, hc *http.Client, base string) (*oauthMeta, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/oauth-authorization-server", nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("no OAuth sign-in at %s (HTTP %d)", base, resp.StatusCode)
	}
	var m oauthMeta
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("OAuth metadata at %s is not JSON: %w", base, err)
	}
	if m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" {
		return nil, fmt.Errorf("OAuth metadata at %s has no authorization or token endpoint", base)
	}
	for _, e := range []string{m.AuthorizationEndpoint, m.TokenEndpoint, m.RegistrationEndpoint, m.RevocationEndpoint} {
		if e == "" {
			continue
		}
		if err := sameOriginOrLoopback(e, base, m.Issuer); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

// sameOriginOrLoopback refuses to send a token anywhere but the API's own
// origin (or its issuer's), over https unless it is a loopback test server.
// A tampered metadata document or credentials file cannot redirect the
// refresh token to another host.
func sameOriginOrLoopback(endpoint, base, issuer string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("refusing OAuth endpoint %q: not an absolute URL", endpoint)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return fmt.Errorf("refusing OAuth endpoint %s: not https", u.Host)
	}
	for _, o := range []string{base, issuer} {
		if p, err := url.Parse(o); err == nil && p.Host != "" && strings.EqualFold(p.Host, u.Host) {
			return nil
		}
	}
	return fmt.Errorf("refusing OAuth endpoint on %s: not the API's host", u.Host)
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// postToken calls the token endpoint. status is the HTTP status (0 on a
// transport failure), so the caller can tell a refusal from an outage. The
// error never carries the response body: it may hold a token.
func postToken(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*tokenResponse, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var t tokenResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t)
	if resp.StatusCode != http.StatusOK || t.AccessToken == "" {
		code := t.Error
		if code == "" {
			code = "HTTP " + strconv.Itoa(resp.StatusCode)
		}
		if t.ErrorDescription != "" {
			code += ": " + t.ErrorDescription
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			status = 502 // 200 without a token: the server's fault, not ours
		}
		return nil, status, errors.New(code)
	}
	return &t, resp.StatusCode, nil
}

// clientKnown tells a client the server still has (400 invalid_grant to a bogus
// refresh token) from one it forgot (401 invalid_client). No side effect.
// Anything unclear counts as known; the sign-in then reports the real problem.
func clientKnown(ctx context.Context, hc *http.Client, m *oauthMeta, clientID string) bool {
	_, _, err := postToken(ctx, hc, m.TokenEndpoint, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {"probe"}, "client_id": {clientID},
	})
	return err == nil || !strings.HasPrefix(err.Error(), "invalid_client")
}

func registerClient(ctx context.Context, hc *http.Client, m *oauthMeta, redirectURI string) (*oauthClient, error) {
	if m.RegistrationEndpoint == "" {
		return nil, errors.New("this server does not offer client registration; use an API key (GRAPH_API_KEY or --api-key-file)")
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "graphwatch",
		"client_uri":                 "https://github.com/instruxi-io/graphwatch",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"software_id":                "graphwatch",
		"software_version":           version,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.RegistrationEndpoint, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client registration: %w", err)
	}
	defer resp.Body.Close()
	var r struct {
		ClientID string `json:"client_id"`
		Scope    string `json:"scope"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r)
	if resp.StatusCode/100 != 2 || r.ClientID == "" {
		return nil, fmt.Errorf("client registration failed (HTTP %d)", resp.StatusCode)
	}
	return &oauthClient{ClientID: r.ClientID, Ceiling: r.Scope}, nil
}

func ceilingCovers(c *oauthClient, scope string) bool {
	if c == nil || c.Ceiling == "" {
		return true
	}
	held := map[string]bool{}
	for _, s := range strings.Fields(c.Ceiling) {
		held[s] = true
	}
	for _, s := range strings.Fields(scope) {
		if s != "offline_access" && !held[s] {
			return false
		}
	}
	return true
}

func pickScopes(m *oauthMeta, want []string) string {
	if len(m.ScopesSupported) == 0 {
		return strings.Join(want, " ")
	}
	offered := map[string]bool{}
	for _, s := range m.ScopesSupported {
		offered[s] = true
	}
	var out []string
	for _, s := range want {
		if offered[s] {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on a supported OS
	}
	return b64url(b)
}

// ---- the sign-in ------------------------------------------------------------

type loginOptions struct {
	base      string
	scopes    []string
	newApp    bool
	noBrowser bool
	timeout   time.Duration
	hc        *http.Client
	in        io.Reader // where a pasted redirect URL is read from (SSH)
	out       io.Writer
	openURL   func(string) error
	now       func() time.Time
}

type loginResult struct {
	session    *loginSession
	notGranted []string
}

// browserLogin runs the loopback authorization-code flow and returns the new
// session. The browser normally lands on the loopback listener; when it cannot
// (graphwatch on a remote machine over SSH) the user pastes the address the
// browser ended on, and the code is read from that.
func browserLogin(ctx context.Context, dir string, o loginOptions) (*loginResult, error) {
	hc := o.hc
	if hc == nil {
		hc = newOAuthHTTP()
	}
	now := o.now
	if now == nil {
		now = time.Now
	}
	meta, err := discover(ctx, hc, o.base)
	if err != nil {
		return nil, err
	}
	scope := pickScopes(meta, o.scopes)
	if scope == "" {
		return nil, fmt.Errorf("the server offers none of the scopes asked for (%s)", strings.Join(o.scopes, " "))
	}
	verifier := randomString(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := b64url(sum[:])
	state := randomString(16)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not listen on 127.0.0.1 for the sign-in redirect: %w", err)
	}
	defer ln.Close()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	type answer struct {
		code string
		err  error
	}
	got := make(chan answer, 2)
	check := func(q url.Values) answer {
		if e := q.Get("error"); e != "" {
			if d := q.Get("error_description"); d != "" {
				e += " — " + d
			}
			return answer{err: fmt.Errorf("sign-in refused: %s", e)}
		}
		if q.Get("state") != state {
			return answer{err: errors.New("state mismatch — that redirect did not come from this sign-in")}
		}
		if q.Get("code") == "" {
			return answer{err: errors.New("the redirect carried no code")}
		}
		return answer{code: q.Get("code")}
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		a := check(r.URL.Query())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if a.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, callbackPage("Sign-in did not complete", "Return to your terminal for details."))
		} else {
			io.WriteString(w, callbackPage("Signed in to graphwatch", "You can close this tab and return to your terminal."))
		}
		select {
		case got <- a:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	store, err := readLoginStore(dir)
	if err != nil {
		return nil, err
	}
	if store.Clients == nil {
		store.Clients = map[string]*oauthClient{}
	}
	if store.Sessions == nil {
		store.Sessions = map[string]*loginSession{}
	}
	// One registered client per issuer, reused: consent is per account and
	// client, and a loopback redirect's port may differ from the registered
	// one (RFC 8252 §7.3), so one registration serves every later sign-in.
	client := store.Clients[meta.Issuer]
	if o.newApp || client == nil || !ceilingCovers(client, scope) || !clientKnown(ctx, hc, meta, client.ClientID) {
		if client, err = registerClient(ctx, hc, meta, redirectURI); err != nil {
			return nil, err
		}
		store.Clients[meta.Issuer] = client
	}

	au, _ := url.Parse(meta.AuthorizationEndpoint)
	q := au.Query()
	q.Set("response_type", "code")
	q.Set("client_id", client.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", scope)
	q.Set("state", state)
	q.Set("resource", o.base)
	au.RawQuery = q.Encode()

	fmt.Fprintf(o.out, "Signing in to %s\n\nOpen this address to sign in:\n\n  %s\n\n", o.base, au.String())
	opened := false
	if !o.noBrowser && o.openURL != nil {
		opened = o.openURL(au.String()) == nil
	}
	if opened {
		fmt.Fprintln(o.out, "Your browser should have opened it.")
	}
	if o.in != nil {
		fmt.Fprintln(o.out, "If the browser is on another machine, sign in there, then paste the address it ends on")
		fmt.Fprintln(o.out, "(http://127.0.0.1:…/callback?code=…, even if the page does not load) and press enter:")
		go func() {
			sc := bufio.NewScanner(o.in)
			sc.Buffer(make([]byte, 64*1024), 64*1024)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				u, err := url.Parse(line)
				if err != nil || u.RawQuery == "" {
					fmt.Fprintln(o.out, "That is not the address the browser ended on; paste the whole URL:")
					continue
				}
				select {
				case got <- check(u.Query()):
				default:
				}
				return
			}
		}()
	}

	timeout := o.timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	var a answer
	select {
	case a = <-got:
	case <-time.After(timeout):
		return nil, errors.New("timed out waiting for the browser sign-in")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if a.err != nil {
		return nil, a.err
	}

	tok, _, err := postToken(ctx, hc, meta.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {a.code},
		"redirect_uri":  {redirectURI},
		"client_id":     {client.ClientID},
		"code_verifier": {verifier},
		"resource":      {o.base},
	})
	if err != nil {
		return nil, fmt.Errorf("code redemption failed: %w", err)
	}
	t := now()
	s := &loginSession{
		Issuer:             meta.Issuer,
		ClientID:           client.ClientID,
		TokenEndpoint:      meta.TokenEndpoint,
		RevocationEndpoint: meta.RevocationEndpoint,
		SignedInAt:         t.UTC().Format(time.RFC3339),
		Scope:              scope,
	}
	s.apply(tok, t)

	// Revoke the session this one replaces, best effort: it would otherwise
	// live on, unused, until it expired.
	if old := store.Sessions[o.base]; old != nil && old.RefreshToken != "" {
		revoke(ctx, hc, old)
	}
	store.Sessions[o.base] = s
	if err := writeLoginStore(dir, store); err != nil {
		return nil, fmt.Errorf("signed in, but could not save the session: %w", err)
	}
	return &loginResult{session: s, notGranted: missingScopes(scope, s.Scope)}, nil
}

func missingScopes(requested, granted string) []string {
	g := map[string]bool{}
	for _, s := range strings.Fields(granted) {
		g[s] = true
	}
	var out []string
	for _, s := range strings.Fields(requested) {
		if s != "offline_access" && !g[s] {
			out = append(out, s)
		}
	}
	return out
}

// revoke tells the server a refresh token is dead (RFC 7009). Best effort.
func revoke(ctx context.Context, hc *http.Client, s *loginSession) error {
	if s.RevocationEndpoint == "" || s.RefreshToken == "" {
		return errors.New("the server offers no revocation endpoint")
	}
	if err := sameOriginOrLoopback(s.RevocationEndpoint, s.Issuer, ""); err != nil {
		return err
	}
	form := url.Values{"token": {s.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {s.ClientID}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.RevocationEndpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func callbackPage(title, body string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` +
		title + `</title><style>body{font:16px/1.5 system-ui,sans-serif;background:#f5f6f8;color:#16181d;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center;padding:24px}.c{background:#fff;border:1px solid #dfe2e8;border-radius:10px;padding:28px;max-width:420px;width:100%}h1{font-size:20px;margin:0 0 8px}p{color:#5b6070;margin:0}@media(prefers-color-scheme:dark){body{background:#111318;color:#e6e8ee}.c{background:#1a1d24;border-color:#2b2f3a}p{color:#a0a6b4}}</style></head><body><div class="c"><h1>` +
		title + `</h1><p>` + body + `</p></div></body></html>`
}

// openBrowser opens url in the desktop browser. It fails (so the caller says
// to open it by hand) when there is no desktop: over SSH, or a Linux session
// with no display.
func openBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		if os.Getenv("SSH_CONNECTION") != "" || (os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "") {
			return errors.New("no desktop")
		}
		return exec.Command("xdg-open", u).Start()
	}
}

// ---- the commands -----------------------------------------------------------

func loginBase(base string) (dir, resolved string, err error) {
	home, _ := os.UserHomeDir()
	get := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	dir = loginDir(get, home)
	if dir == "" {
		return "", "", errors.New("no home or config directory to keep the sign-in in; set GRAPHWATCH_CONFIG_DIR")
	}
	return dir, resolveBaseURL(get, authFlags{base: base}, readEnforcerCredentials(get, home)), nil
}

func runLogin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("graphwatch login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "api origin to sign in to (default: GRAPH_BASE_URL, the plugin's saved base_url, then https://api.instruxi.dev)")
	readOnly := fs.Bool("read-only", false, "ask only for enforcer:read: watch and list, never answer")
	scope := fs.String("scope", "", "ask for exactly these scopes (space or comma separated)")
	noBrowser := fs.Bool("no-browser", false, "print the address instead of opening a browser (SSH, containers)")
	newApp := fs.Bool("new-app", false, "register a fresh OAuth client instead of reusing this machine's")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the browser sign-in")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: graphwatch login [--base url] [--read-only | --scope \"a b\"] [--no-browser]")
		return exitUsage
	}
	scopes := defaultLoginScopes
	switch {
	case *scope != "" && *readOnly:
		fmt.Fprintln(stderr, "graphwatch: --scope and --read-only do not go together")
		return exitUsage
	case *scope != "":
		scopes = append(strings.Fields(strings.ReplaceAll(*scope, ",", " ")), "offline_access")
	case *readOnly:
		scopes = readOnlyLoginScopes
	}
	dir, b, err := loginBase(*base)
	if err != nil {
		fmt.Fprintln(stderr, "graphwatch:", err)
		return exitUsage
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var in io.Reader
	if isTerminal(os.Stdin) {
		in = os.Stdin
	}
	res, err := browserLogin(ctx, dir, loginOptions{
		base: b, scopes: scopes, newApp: *newApp, noBrowser: *noBrowser, timeout: *timeout,
		in: in, out: stderr, openURL: openBrowser,
	})
	if err != nil {
		fmt.Fprintln(stderr, "graphwatch: sign-in failed:", err)
		return exitRuntime
	}
	// Prove it: ask the API who this is, with the credential the commands use.
	cred := &loginCredential{dir: dir, base: b, hc: newOAuthHTTP()}
	who := "-"
	if d, err := newClient(b, cred).status(ctx); err == nil {
		who = pick(d, "email", "account", "account_id")
		tenant := pick(d, "tenant", "tenant_name", "tenant_id")
		role := pick(d, "role")
		res.session.Account = who
		if store, err := readLoginStore(dir); err == nil && store.Sessions[b] != nil {
			store.Sessions[b].Account = who
			_ = writeLoginStore(dir, store)
		}
		fmt.Fprintf(stdout, "Signed in to %s as %s (tenant %s, role %s).\n", b, who, tenant, role)
	} else {
		fmt.Fprintf(stdout, "Signed in to %s, but the API did not accept the token yet: %v\n", b, err)
	}
	fmt.Fprintf(stdout, "Scopes: %s\n", res.session.Scope)
	if len(res.notGranted) > 0 {
		fmt.Fprintf(stdout, "Not granted: %s (your role may not allow them; try --new-app if the server added them recently)\n", strings.Join(res.notGranted, " "))
	}
	if res.session.RefreshExpiresAt != "" {
		fmt.Fprintf(stdout, "The sign-in refreshes itself until %s; then run graphwatch login again.\n", res.session.RefreshExpiresAt)
	}
	fmt.Fprintf(stdout, "Saved in %s\n", filepath.Join(dir, "credentials.json"))
	return exitOK
}

func runLogout(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("graphwatch logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "api origin to sign out of (default: as for login)")
	all := fs.Bool("all", false, "sign out of every API graphwatch is signed in to")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	dir, b, err := loginBase(*base)
	if err != nil {
		fmt.Fprintln(stderr, "graphwatch:", err)
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	release, err := acquireLock(ctx, dir)
	if err != nil {
		fmt.Fprintln(stderr, "graphwatch:", err)
		return exitRuntime
	}
	defer release()
	store, err := readLoginStore(dir)
	if err != nil {
		fmt.Fprintln(stderr, "graphwatch:", err)
		return exitRuntime
	}
	targets := []string{b}
	if *all {
		targets = targets[:0]
		for k := range store.Sessions {
			targets = append(targets, k)
		}
	}
	hc := newOAuthHTTP()
	n := 0
	for _, t := range targets {
		s := store.Sessions[t]
		if s == nil {
			continue
		}
		n++
		if err := revoke(ctx, hc, s); err != nil && s.RefreshToken != "" {
			fmt.Fprintf(stderr, "graphwatch: %s: could not revoke on the server (%v); forgetting it here anyway\n", t, err)
		}
		delete(store.Sessions, t)
		fmt.Fprintf(stdout, "Signed out of %s.\n", t)
	}
	if n == 0 {
		fmt.Fprintf(stdout, "graphwatch was not signed in to %s.\n", b)
		return exitOK
	}
	if err := writeLoginStore(dir, store); err != nil {
		fmt.Fprintln(stderr, "graphwatch:", err)
		return exitRuntime
	}
	return exitOK
}
