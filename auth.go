package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// credential supplies the headers that authenticate a request. It never logs
// or returns anything but the headers themselves.
//
// fresh asks for new headers after a 401: an OAuth access token expires, and
// the helper refreshes it (the refresh_token grant) the next time it runs.
type credential interface {
	headers(ctx context.Context, fresh bool) (map[string]string, error)
}

// apiKey is the long-lived X-API-Key from GRAPH_API_KEY.
type apiKey string

func (k apiKey) headers(context.Context, bool) (map[string]string, error) {
	return map[string]string{"X-API-Key": string(k)}, nil
}

// helper runs a command that prints the auth headers as a JSON object, e.g.
// the enforcer plugin's bin/enforcer-headers.mjs, which reads the OAuth
// session /enforcer:login saved in ~/.enforcer/credentials.json and refreshes
// it when it is close to expiry. The same helper Claude Code runs for the
// Enforcer MCP server, so one sign-in covers both. Run through `sh -c` so a
// `~` or quoted path in GRAPH_AUTH_HELPER works as typed.
type helper struct {
	cmd string

	mu     sync.Mutex
	cached map[string]string
}

func (h *helper) headers(ctx context.Context, fresh bool) (map[string]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cached != nil && !fresh {
		return h.cached, nil
	}
	got, err := runHelper(ctx, h.cmd)
	if err != nil {
		return nil, err
	}
	h.cached = got
	return got, nil
}

// errSignedOut is what an empty helper answer means: the enforcer helper
// prints {} when there is no session (or its refresh failed).
var errSignedOut = errors.New("the auth helper returned no headers — signed out? run /enforcer:login")

func runHelper(ctx context.Context, cmd string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	var out, stderr bytes.Buffer
	c.Stdout, c.Stderr = &out, &stderr
	if err := c.Run(); err != nil {
		// stderr only: stdout may hold a token.
		return nil, fmt.Errorf("auth helper failed: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &raw); err != nil {
		return nil, errors.New("auth helper did not print a JSON object of headers")
	}
	h := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			h[k] = s
		}
	}
	if len(h) == 0 {
		return nil, errSignedOut
	}
	return h, nil
}

// envLookup reads one environment variable; os.LookupEnv in the program, a map
// in tests, so resolution never depends on the environment it was tested in.
type envLookup func(string) (string, bool)

// authFlags are the command-line inputs to resolution. There is deliberately
// no --api-key flag: argv is visible to every user through ps.
type authFlags struct {
	apiKeyFile string // --api-key-file
	base       string // --base
}

const defaultBaseURL = "https://api.instruxi.dev"

// credentialSources is the resolution order, in words, for the error a user
// sees when none of them yields a credential.
var credentialSources = []string{
	"--api-key-file <path>",
	"GRAPH_AUTH_HELPER (a command printing auth headers as JSON)",
	"GRAPH_API_KEY",
	"ENFORCER_API_KEY",
	"the enforcer plugin's sign-in (~/.claude/plugins/cache/*/enforcer/*/bin/enforcer-headers.mjs)",
	"enforcer.api_key in ~/.enforcer/credentials.json ($ENFORCER_HOME overrides the directory)",
}

// resolveCredential picks the one credential graphwatch uses, first match
// wins, in the order of credentialSources. source is a label for the user and
// never carries key or token text. An OAuth session is only ever reached
// through the plugin's helper: the helper rotates a single-use refresh token
// and writes it back, so graphwatch refreshing it itself, or writing
// credentials.json, would sign the plugin out.
func resolveCredential(env envLookup, home string, flags authFlags) (cred credential, source string, base string, err error) {
	get := func(k string) string {
		v, _ := env(k)
		return strings.TrimSpace(v)
	}
	file := readEnforcerCredentials(get, home)

	base = resolveBaseURL(get, flags, file)

	if flags.apiKeyFile != "" {
		b, err := os.ReadFile(flags.apiKeyFile)
		if err != nil {
			return nil, "", base, fmt.Errorf("--api-key-file: %w", err)
		}
		key := strings.TrimSpace(string(b))
		if key == "" {
			return nil, "", base, fmt.Errorf("--api-key-file %s is empty", flags.apiKeyFile)
		}
		return apiKey(key), "--api-key-file", base, nil
	}
	if cmd := get("GRAPH_AUTH_HELPER"); cmd != "" {
		return &helper{cmd: cmd}, "GRAPH_AUTH_HELPER", base, nil
	}
	if key := get("GRAPH_API_KEY"); key != "" {
		return apiKey(key), "GRAPH_API_KEY", base, nil
	}
	if key := get("ENFORCER_API_KEY"); key != "" {
		return apiKey(key), "ENFORCER_API_KEY", base, nil
	}
	if path, version := findPluginHelper(home); path != "" {
		return &helper{cmd: "node " + shellQuote(path)},
			"plugin sign-in (enforcer " + version + " helper)", base, nil
	}
	if file.Enforcer.APIKey != "" {
		return apiKey(file.Enforcer.APIKey), "~/.enforcer/credentials.json (enforcer.api_key)", base, nil
	}
	var b strings.Builder
	b.WriteString("graphwatch: no credential found. Looked, in order, at:\n")
	for i, s := range credentialSources {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, s)
	}
	b.WriteString("Sign in with /enforcer:login in Claude Code, or set GRAPH_API_KEY.")
	return nil, "", base, errors.New(b.String())
}

// resolveBaseURL: --base, GRAPH_BASE_URL, the plugin's saved base_url, then
// the public API.
func resolveBaseURL(get func(string) string, flags authFlags, file enforcerCredentials) string {
	for _, b := range []string{flags.base, get("GRAPH_BASE_URL"), file.Enforcer.BaseURL} {
		if b = strings.TrimRight(strings.TrimSpace(b), "/"); b != "" {
			return b
		}
	}
	return defaultBaseURL
}

// enforcerCredentials is the part of the plugin's credentials.json graphwatch
// reads. The oauth block is left alone on purpose: only the helper touches it.
type enforcerCredentials struct {
	Enforcer struct {
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	} `json:"enforcer"`
}

// readEnforcerCredentials reads, never writes, $ENFORCER_HOME/credentials.json
// (default ~/.enforcer). A missing or unreadable file is no credential, not an
// error: it is the last of several places to look.
func readEnforcerCredentials(get func(string) string, home string) enforcerCredentials {
	var c enforcerCredentials
	dir := get("ENFORCER_HOME")
	if dir == "" {
		if home == "" {
			return c
		}
		dir = filepath.Join(home, ".enforcer")
	}
	b, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c)
	return c
}

// findPluginHelper returns the enforcer plugin's header helper of the highest
// installed version, compared numerically so 1.10.0 beats 1.9.0, across every
// marketplace in the Claude Code plugin cache.
func findPluginHelper(home string) (path, version string) {
	if home == "" {
		return "", ""
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude", "plugins", "cache", "*", "enforcer", "*", "bin", "enforcer-headers.mjs"))
	for _, m := range matches {
		v := filepath.Base(filepath.Dir(filepath.Dir(m)))
		if path == "" || compareVersions(v, version) > 0 {
			path, version = m, v
		}
	}
	return path, version
}

// compareVersions compares dotted versions part by part as numbers. A part
// with no leading digits counts as 0; a pre-release suffix ("1.2.0-rc1") is
// ignored, which is close enough for picking an installed plugin.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = leadingInt(pa[i])
		}
		if i < len(pb) {
			y = leadingInt(pb[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(a, b)
}

func leadingInt(s string) int {
	s = strings.TrimPrefix(s, "v")
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// shellQuote quotes a path for `sh -c`, which the helper runs through.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
