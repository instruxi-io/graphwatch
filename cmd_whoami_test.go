package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretKey = "sk-super-secret-value-123456"

func statusRoutes() map[string]any {
	return map[string]any{"/status": map[string]any{
		"account": "ada@example.com", "tenant": "Acme", "role": "admin", "status": "ok"}}
}

// isolate keeps the developer's real plugin and credentials out of a test.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ENFORCER_HOME", t.TempDir())
	for _, k := range []string{"GRAPH_API_KEY", "ENFORCER_API_KEY", "GRAPH_AUTH_HELPER", "GRAPH_BASE_URL"} {
		t.Setenv(k, "")
	}
}

func TestWhoamiNeverPrintsSecret(t *testing.T) {
	isolate(t)
	f := newFakeAPI(t, statusRoutes())
	t.Setenv("GRAPH_API_KEY", secretKey)
	for _, args := range [][]string{{"whoami", "--base", f.URL}, {"whoami", "--json", "--base", f.URL}} {
		var out, errb bytes.Buffer
		if code, _ := dispatch(args, &out, &errb); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errb.String())
		}
		all := out.String() + errb.String()
		if strings.Contains(all, secretKey) || strings.Contains(all, secretKey[:6]) {
			t.Errorf("%v leaked the key: %s", args, all)
		}
		if !strings.Contains(all, "GRAPH_API_KEY") || !strings.Contains(all, "ada@example.com") ||
			!strings.Contains(all, "Acme") || !strings.Contains(all, "admin") {
			t.Errorf("%v missing fields: %s", args, all)
		}
	}
	f.onlyReads(t)
}

func TestWhoamiJSON(t *testing.T) {
	f := newFakeAPI(t, statusRoutes())
	stdout, _, code := runCLI(t, f, "whoami", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(stdout), &m); err != nil {
		t.Fatal(err, stdout)
	}
	if m["credential_source"] != "GRAPH_API_KEY" || m["role"] != "admin" {
		t.Errorf("got %v", m)
	}
}

func TestWhoamiSignedOut(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"success":false,"error":{"code":"unauthorized","message":"bad"}}`))
	}))
	defer srv.Close()
	t.Setenv("GRAPH_API_KEY", secretKey)
	var out, errb bytes.Buffer
	code, _ := dispatch([]string{"whoami", "--base", srv.URL}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), "signed out or key rejected") ||
		!strings.Contains(errb.String(), "GRAPH_API_KEY") || strings.Contains(errb.String(), secretKey) {
		t.Errorf("exit %d: %s", code, errb.String())
	}
}

// The command path resolves through resolveCredential: --api-key-file beats
// GRAPH_API_KEY and is what the server receives; nothing resolvable exits 2.
func TestMainUsesResolveCredential(t *testing.T) {
	isolate(t)
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-API-Key")
		w.Write([]byte(`{"success":true,"data":{"role":"admin"}}`))
	}))
	defer srv.Close()
	kf := filepath.Join(t.TempDir(), "key")
	os.WriteFile(kf, []byte("file-key\n"), 0o600)
	t.Setenv("GRAPH_API_KEY", "env-key")
	var out, errb bytes.Buffer
	if code, _ := dispatch([]string{"whoami", "--json", "--api-key-file", kf, "--base", srv.URL}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got != "file-key" || !strings.Contains(out.String(), `"credential_source": "--api-key-file"`) {
		t.Errorf("key %q, out %s", got, out.String())
	}

	t.Setenv("GRAPH_API_KEY", "")
	out.Reset()
	errb.Reset()
	if code, _ := dispatch([]string{"whoami", "--base", srv.URL}, &out, &errb); code != 2 ||
		!strings.Contains(errb.String(), "no credential found") {
		t.Errorf("exit %d: %s", code, errb.String())
	}
}
