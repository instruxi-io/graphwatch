package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	secretFileKey   = "sk-from-file-0001"
	secretGraphKey  = "sk-graph-0002"
	secretEnfKey    = "sk-enforcer-0003"
	secretStoredKey = "sk-stored-0004"
	secretToken     = "eyJ-oauth-access-0005"
)

func mapEnv(m map[string]string) envLookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// installPlugin puts a fake enforcer-headers.mjs under home's plugin cache.
// Its body is never interpreted by real node: fakeNode below stands in.
func installPlugin(t *testing.T, home, marketplace, version string) string {
	p := filepath.Join(home, ".claude", "plugins", "cache", marketplace, "enforcer", version, "bin", "enforcer-headers.mjs")
	writeFile(t, p, `{"Authorization":"Bearer `+secretToken+`-`+version+`"}`, 0o644)
	return p
}

// fakeNode puts a `node` on PATH that prints its script argument's contents,
// so the resolved helper command runs end to end without a real node.
func fakeNode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "node"), "#!/bin/sh\ncat \"$1\"\n", 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeStored(t *testing.T, dir, key, base string) {
	writeFile(t, filepath.Join(dir, "credentials.json"),
		`{"enforcer":{"api_key":"`+key+`","base_url":"`+base+`","oauth":{"access_token":"`+secretToken+`","refresh_token":"r","expires_at":1}}}`, 0o600)
}

func headersOf(t *testing.T, c credential) map[string]string {
	t.Helper()
	h, err := c.headers(context.Background(), false)
	if err != nil {
		t.Fatalf("headers: %v", err)
	}
	return h
}

// TestResolveOrder removes the winning source one at a time and checks the
// next one in the documented order takes over.
func TestResolveOrder(t *testing.T) {
	fakeNode(t)
	home := t.TempDir()
	installPlugin(t, home, "instruxi", "1.1.0")
	writeStored(t, filepath.Join(home, ".enforcer"), secretStoredKey, "")
	keyFile := filepath.Join(t.TempDir(), "key")
	writeFile(t, keyFile, secretFileKey+"\n", 0o600)
	helperCmd := fakeHelper(t)

	env := map[string]string{
		"GRAPH_AUTH_HELPER": helperCmd,
		"GRAPH_API_KEY":     secretGraphKey,
		"ENFORCER_API_KEY":  secretEnfKey,
	}
	flags := authFlags{apiKeyFile: keyFile}

	steps := []struct {
		source string
		check  func(credential)
		drop   func()
	}{
		{"--api-key-file", func(c credential) { wantKey(t, c, secretFileKey) }, func() { flags.apiKeyFile = "" }},
		{"GRAPH_AUTH_HELPER", func(c credential) {
			if _, ok := c.(*helper); !ok {
				t.Fatalf("GRAPH_AUTH_HELPER gave %T", c)
			}
		}, func() { delete(env, "GRAPH_AUTH_HELPER") }},
		{"GRAPH_API_KEY", func(c credential) { wantKey(t, c, secretGraphKey) }, func() { delete(env, "GRAPH_API_KEY") }},
		{"ENFORCER_API_KEY", func(c credential) { wantKey(t, c, secretEnfKey) }, func() { delete(env, "ENFORCER_API_KEY") }},
		{"plugin sign-in (enforcer 1.1.0 helper)", func(c credential) {
			if got := headersOf(t, c)["Authorization"]; got != "Bearer "+secretToken+"-1.1.0" {
				t.Fatalf("plugin helper headers: %q", got)
			}
		}, func() { os.RemoveAll(filepath.Join(home, ".claude")) }},
		{"~/.enforcer/credentials.json (enforcer.api_key)", func(c credential) { wantKey(t, c, secretStoredKey) }, func() { os.RemoveAll(filepath.Join(home, ".enforcer")) }},
	}
	for _, s := range steps {
		c, source, _, err := resolveCredential(mapEnv(env), home, flags)
		if err != nil {
			t.Fatalf("want %s, got error %v", s.source, err)
		}
		if source != s.source {
			t.Fatalf("source = %q, want %q", source, s.source)
		}
		s.check(c)
		s.drop()
	}

	_, _, _, err := resolveCredential(mapEnv(env), home, flags)
	if err == nil {
		t.Fatal("nothing left, want an error")
	}
	for _, want := range []string{"--api-key-file", "GRAPH_AUTH_HELPER", "GRAPH_API_KEY", "ENFORCER_API_KEY", "enforcer-headers.mjs", "credentials.json", "/enforcer:login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%v", want, err)
		}
	}
}

func wantKey(t *testing.T, c credential, key string) {
	t.Helper()
	if got := headersOf(t, c)["X-API-Key"]; got != key {
		t.Fatalf("X-API-Key = %q, want %q", got, key)
	}
}

func TestPluginHelperVersionPick(t *testing.T) {
	fakeNode(t)
	home := t.TempDir()
	installPlugin(t, home, "instruxi", "1.9.0")
	installPlugin(t, home, "instruxi", "1.10.0")
	installPlugin(t, home, "other-market", "1.2.3")
	installPlugin(t, home, "instruxi", "0.99.99")

	c, source, _, err := resolveCredential(mapEnv(nil), home, authFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if source != "plugin sign-in (enforcer 1.10.0 helper)" {
		t.Fatalf("source = %q", source)
	}
	if got := headersOf(t, c)["Authorization"]; got != "Bearer "+secretToken+"-1.10.0" {
		t.Fatalf("ran the wrong helper: %q", got)
	}

	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1}, {"1.9.0", "1.10.0", -1}, {"2.0", "1.99.99", 1},
		{"1.2.0", "1.2.0", 0}, {"1.2.1", "1.2", 1},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCredentialsFileApiKey(t *testing.T) {
	home := t.TempDir()
	writeStored(t, filepath.Join(home, ".enforcer"), secretStoredKey, "")
	c, source, _, err := resolveCredential(mapEnv(nil), home, authFlags{})
	if err != nil || source != "~/.enforcer/credentials.json (enforcer.api_key)" {
		t.Fatalf("source %q err %v", source, err)
	}
	wantKey(t, c, secretStoredKey)

	// ENFORCER_HOME moves the directory, as it does for the plugin.
	other := t.TempDir()
	writeStored(t, other, "sk-elsewhere", "")
	c, _, _, err = resolveCredential(mapEnv(map[string]string{"ENFORCER_HOME": other}), home, authFlags{})
	if err != nil {
		t.Fatal(err)
	}
	wantKey(t, c, "sk-elsewhere")

	// An OAuth-only file is not a credential by itself: only the helper may
	// use (and rotate) that token. Nothing is written back either.
	oauthOnly := t.TempDir()
	writeStored(t, oauthOnly, "", "")
	before, _ := os.ReadFile(filepath.Join(oauthOnly, "credentials.json"))
	if _, _, _, err := resolveCredential(mapEnv(map[string]string{"ENFORCER_HOME": oauthOnly}), t.TempDir(), authFlags{}); err == nil {
		t.Fatal("an oauth-only credentials.json resolved without the plugin helper")
	}
	after, _ := os.ReadFile(filepath.Join(oauthOnly, "credentials.json"))
	if string(before) != string(after) {
		t.Fatal("credentials.json was modified")
	}
}

func TestResolveBaseURLOrder(t *testing.T) {
	home := t.TempDir()
	writeStored(t, filepath.Join(home, ".enforcer"), secretStoredKey, "https://stored.example/")
	env := map[string]string{"GRAPH_BASE_URL": "https://env.example"}
	flags := authFlags{base: "https://flag.example"}

	for _, want := range []string{"https://flag.example", "https://env.example", "https://stored.example", defaultBaseURL} {
		_, _, base, err := resolveCredential(mapEnv(env), home, flags)
		if err != nil {
			t.Fatal(err)
		}
		if base != want {
			t.Fatalf("base = %q, want %q", base, want)
		}
		switch want {
		case "https://flag.example":
			flags.base = ""
		case "https://env.example":
			delete(env, "GRAPH_BASE_URL")
		case "https://stored.example":
			writeStored(t, filepath.Join(home, ".enforcer"), secretStoredKey, "")
		}
	}
}

func TestResolveSourceLabelNoSecret(t *testing.T) {
	fakeNode(t)
	home := t.TempDir()
	installPlugin(t, home, "instruxi", "1.1.0")
	writeStored(t, filepath.Join(home, ".enforcer"), secretStoredKey, "")
	keyFile := filepath.Join(t.TempDir(), "key")
	writeFile(t, keyFile, secretFileKey, 0o600)

	cases := []struct {
		env   map[string]string
		flags authFlags
	}{
		{nil, authFlags{apiKeyFile: keyFile}},
		{map[string]string{"GRAPH_AUTH_HELPER": "echo '{\"X-API-Key\":\"" + secretGraphKey + "\"}'"}, authFlags{}},
		{map[string]string{"GRAPH_API_KEY": secretGraphKey}, authFlags{}},
		{map[string]string{"ENFORCER_API_KEY": secretEnfKey}, authFlags{}},
		{nil, authFlags{}},
	}
	secrets := []string{secretFileKey, secretGraphKey, secretEnfKey, secretStoredKey, secretToken, "sk-", "eyJ"}
	for _, tc := range cases {
		_, source, _, err := resolveCredential(mapEnv(tc.env), home, tc.flags)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if strings.Contains(source, s) {
				t.Fatalf("source label %q leaks %q", source, s)
			}
		}
	}

	// Nor does the failure message, even with an empty key file named.
	empty := filepath.Join(t.TempDir(), "empty")
	writeFile(t, empty, "\n", 0o600)
	_, _, _, err := resolveCredential(mapEnv(nil), t.TempDir(), authFlags{apiKeyFile: empty})
	if err == nil {
		t.Fatal("an empty --api-key-file resolved")
	}
	for _, s := range secrets {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("error leaks %q: %v", s, err)
		}
	}
}
