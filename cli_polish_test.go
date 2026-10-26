package main

import (
	"bytes"
	"flag"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// helpFor returns what `graphwatch help <name>` prints.
func helpFor(t *testing.T, name string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := runHelp([]string{name}, &out, &errb); code != exitOK {
		t.Fatalf("help %s exited %d: %s", name, code, errb.String())
	}
	return out.String()
}

// The flag package takes a backticked word in a usage string as the value's
// placeholder name and prints it in place of the real type.
func TestHelpHasNoBacktickPlaceholders(t *testing.T) {
	for name := range commands {
		if got := helpFor(t, name); strings.Contains(got, "`") {
			t.Errorf("help %s contains a backtick:\n%s", name, got)
		}
	}
	for _, bad := range []string{"-tier type", "-all next"} {
		for _, c := range []string{"work", "query"} {
			if strings.Contains(helpFor(t, c), bad) {
				t.Errorf("help %s still prints %q", c, bad)
			}
		}
	}
	for _, p := range queryParams {
		d := queryParamHelp[p]
		if d == "" || strings.HasPrefix(d, "query parameter") {
			t.Errorf("query flag %s has no real description (%q)", p, d)
		}
	}
}

// Every flag a command registers, and every flag it parses by hand, must be
// listed by its help.
func TestEveryRegisteredFlagAppearsInHelp(t *testing.T) {
	for name := range commands {
		h := helpFor(t, name)
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		newGlobalFlags(fs)
		if setup := commandFlags[name]; setup != nil {
			setup(fs)
		}
		fs.VisitAll(func(f *flag.Flag) {
			if !strings.Contains(h, "-"+f.Name) {
				t.Errorf("help %s does not list --%s", name, f.Name)
			}
		})
	}
	byHand := map[string][]string{"access": {"full-ids"}, "review": {"open", "all"}, "runs": {"full"}, "work": {"for", "tier"}}
	for c, flags := range byHand {
		for _, f := range flags {
			if !strings.Contains(helpFor(t, c), "-"+f) {
				t.Errorf("help %s does not list --%s", c, f)
			}
		}
	}
	var b bytes.Buffer
	fs := flag.NewFlagSet("graphwatch", flag.ContinueOnError)
	fs.SetOutput(&b)
	defineWatchFlags(fs)
	fs.PrintDefaults()
	for _, f := range []string{"json", "limit", "no-color", "timeout", "fail"} {
		if !strings.Contains(b.String(), "-"+f) {
			t.Errorf("the watch view's --help does not list --%s", f)
		}
	}
}

// A flag that exists but is not in the README is a flag nobody finds. The
// watch flags are checked against the Flags table; each command's own flags,
// including ones it parses itself (commandNotes), against that command's row
// in the Commands table or the table that follows the sentence "`<cmd>` also
// takes", so one mention elsewhere cannot cover them.
func TestREADMEListsEveryFlag(t *testing.T) {
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	section := func(name string) string {
		var sb strings.Builder
		for i, l := range lines {
			switch {
			case strings.HasPrefix(l, "| `graphwatch "+name+"`"):
				sb.WriteString(l + "\n")
			case strings.HasPrefix(l, "`"+name+"` also takes"), l == "### `"+name+"` flags":
				// The table that follows, up to the next heading.
				for _, m := range lines[i+1:] {
					if strings.HasPrefix(m, "#") {
						break
					}
					sb.WriteString(m + "\n")
				}
			}
		}
		return sb.String()
	}
	var missing []string
	w := flag.NewFlagSet("watch", flag.ContinueOnError)
	defineWatchFlags(w)
	flagsTable := string(raw)
	w.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(flagsTable, "`--"+f.Name+"`") {
			missing = append(missing, "watch --"+f.Name)
		}
	})
	noteFlag := regexp.MustCompile(`(?m)^  --([a-z-]+)`)
	for name := range commands {
		own := map[string]bool{}
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		global := flag.NewFlagSet(name, flag.ContinueOnError)
		newGlobalFlags(global)
		newGlobalFlags(fs)
		if setup := commandFlags[name]; setup != nil {
			setup(fs)
		}
		fs.VisitAll(func(f *flag.Flag) {
			if global.Lookup(f.Name) == nil {
				own[f.Name] = true
			}
		})
		for _, m := range noteFlag.FindAllStringSubmatch(commandNotes[name], -1) {
			own[m[1]] = true
		}
		text := section(name)
		for f := range own {
			if !strings.Contains(text, "`--"+f+"`") {
				missing = append(missing, name+" --"+f)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("README.md does not list, in the command's own section: %s", strings.Join(missing, ", "))
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	var out, errb bytes.Buffer
	code, handled := dispatch([]string{"bogus"}, &out, &errb)
	if !handled || code != exitUsage {
		t.Fatalf("code=%d handled=%v, want 2 true", code, handled)
	}
	if !strings.Contains(errb.String(), `unknown command "bogus"`) || !strings.Contains(errb.String(), "commands:") {
		t.Errorf("stderr = %q", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	// A flag is still the watch view's, not an unknown command.
	if _, handled := dispatch([]string{"--graph", "x"}, &out, &errb); handled {
		t.Error("a bare flag must fall through to the watch view")
	}
}

func TestVersionFlagPrintsVersion(t *testing.T) {
	old := version
	defer func() { version = old }()
	for _, args := range [][]string{{"--version"}, {"version"}} {
		var out, errb bytes.Buffer
		if code, handled := dispatch(args, &out, &errb); code != 0 || !handled || out.String() != "graphwatch dev\n" {
			t.Errorf("%v: code=%d handled=%v out=%q", args, code, handled, out.String())
		}
	}
	version = "v1.2.3"
	var out, errb bytes.Buffer
	dispatch([]string{"--version"}, &out, &errb)
	if out.String() != "graphwatch v1.2.3\n" {
		t.Errorf("out = %q", out.String())
	}
}

func TestTerminalTooSmallMessage(t *testing.T) {
	size := func(w, h int) func() (int, int, bool) { return func() (int, int, bool) { return w, h, true } }
	if got := watchTermProblem(true, false, false, size(40, 10)); !strings.HasPrefix(got, "graphwatch needs a terminal of at least 80x24") {
		t.Errorf("40x10: %q", got)
	}
	if got := watchTermProblem(true, false, false, size(80, 24)); got != "" {
		t.Errorf("80x24 must be allowed, got %q", got)
	}
	if got := watchTermProblem(true, false, false, func() (int, int, bool) { return 0, 0, false }); got != "" {
		t.Errorf("unknown size must not block, got %q", got)
	}
	for name, got := range map[string]string{
		"not a tty": watchTermProblem(false, false, false, size(100, 30)),
		"no colour": watchTermProblem(true, true, false, size(100, 30)),
		"dumb":      watchTermProblem(true, false, true, size(100, 30)),
	} {
		if got == "" || strings.Contains(got, "\x1b") {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestClientTimeoutIsTenSeconds(t *testing.T) {
	c := newClient("http://example.invalid", nil)
	if c.http.Timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", c.http.Timeout)
	}
}

func TestUnauthorizedUsesWhoamiWording(t *testing.T) {
	old := credSource
	defer func() { credSource = old }()
	credSource = "GRAPH_API_KEY"
	var b bytes.Buffer
	if code := reportError(&b, &apiError{status: 401, msg: "unauthenticated"}); code != exitRuntime {
		t.Errorf("code = %d", code)
	}
	if got := b.String(); !strings.Contains(got, "signed out or key rejected (credential source: GRAPH_API_KEY)") {
		t.Errorf("got %q", got)
	}
}
