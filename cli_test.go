package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRegistryDispatch(t *testing.T) {
	var gotArgs []string
	var sawKey bool
	name := "zz-test-" + t.Name()
	register(name, "test command", func(ctx context.Context, env *cliEnv, args []string) int {
		gotArgs = args
		sawKey = env.client != nil && env.json && env.limit == 3
		return 7
	})
	t.Cleanup(func() { delete(commands, name) })

	f := newFakeAPI(t, nil)
	_, _, code := runCLI(t, f, name, "--json", "--limit", "3", "pos")
	if code != 7 || len(gotArgs) != 1 || gotArgs[0] != "pos" || !sawKey {
		t.Fatalf("code=%d args=%v env ok=%v", code, gotArgs, sawKey)
	}
	// Bad global flag: usage error, command never runs.
	gotArgs = nil
	_, _, code = runCLI(t, f, name, "--limit", "-1")
	if code != exitUsage || gotArgs != nil {
		t.Fatalf("negative limit: code=%d args=%v", code, gotArgs)
	}
	// No credential anywhere (not even a plugin on this machine): usage error.
	isolate(t)
	var out, errb bytes.Buffer
	if code, _ := dispatch([]string{name}, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "GRAPH_API_KEY") {
		t.Fatalf("no cred: code=%d err=%q", code, errb.String())
	}
}

func TestHelpListsCommands(t *testing.T) {
	name := "zz-help-listed"
	register(name, "a summary line", func(context.Context, *cliEnv, []string) int { return 0 })
	t.Cleanup(func() { delete(commands, name) })

	var out, errb bytes.Buffer
	if code, ok := dispatch([]string{"help"}, &out, &errb); !ok || code != 0 {
		t.Fatalf("help: ok=%v code=%d", ok, code)
	}
	for _, want := range []string{"usage: graphwatch <command> [flags]", name, "a summary line", "--no-color"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code, _ := dispatch([]string{"help", name}, &out, &errb); code != 0 || !strings.Contains(out.String(), "usage: graphwatch "+name) {
		t.Fatalf("help <cmd>: code=%d %q", code, out.String())
	}
	if code, _ := dispatch([]string{"help", "nope"}, &out, &errb); code != exitUsage {
		t.Fatalf("help unknown: code=%d", code)
	}
}

func TestLegacyFlagsStillWatch(t *testing.T) {
	// Bare flags are not a registered name, so main falls through to watchMain.
	for _, args := range [][]string{
		{"--graph", "abc"}, {"--demo"}, {"--demo", "--layout", "mycelium"}, {},
	} {
		if _, handled := dispatch(args, &bytes.Buffer{}, &bytes.Buffer{}); handled {
			t.Errorf("%v must fall through to the watch path", args)
		}
	}
}

func TestAPIErrorPrintsStatusCodeMessage(t *testing.T) {
	name := "zz-err"
	register(name, "fails", func(ctx context.Context, env *cliEnv, args []string) int {
		var v any
		return reportError(env.errOut, env.client.do(ctx, "GET", "/missing", nil, &v))
	})
	t.Cleanup(func() { delete(commands, name) })
	f := newFakeAPI(t, nil)
	_, stderr, code := runCLI(t, f, name)
	if code != exitRuntime || stderr != "graphwatch: 404 not_found: no such route\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if strings.Contains(stderr, "test-key") {
		t.Fatal("credential leaked")
	}
}
