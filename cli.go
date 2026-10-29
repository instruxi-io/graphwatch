package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Exit codes every command returns.
const (
	exitOK      = 0
	exitRuntime = 1 // runtime or API error
	exitUsage   = 2 // bad flags or arguments
)

// cliEnv is what a command gets: a resolved client and the global flags.
type cliEnv struct {
	client  *client
	base    string
	out     io.Writer
	errOut  io.Writer
	json    bool
	limit   int
	noColor bool
	isTTY   bool
	// fs is the parsed flag set, so a command that declared flags through
	// commandFlags reads them with fs.Lookup(name).Value.String().
	fs     *flag.FlagSet
	source string // credential source label, never a secret
}

type command struct {
	name, summary string
	run           func(ctx context.Context, env *cliEnv, args []string) int
}

var commands = map[string]command{}

// commandFlags lets a command declare its own flags on the shared flag set, so
// they parse in any position (`work --state x`) next to the global ones.
var commandFlags = map[string]func(fs *flag.FlagSet){}

// commandNotes documents flags a command parses itself from its own arguments
// (so they cannot live on the shared flag set) for `help <command>`.
var commandNotes = map[string]string{}

// register is called from a command's init(). A duplicate name is a programming
// error, so it panics at start-up rather than silently shadowing.
func register(name, summary string, run func(ctx context.Context, env *cliEnv, args []string) int) {
	if _, dup := commands[name]; dup {
		panic("graphwatch: command registered twice: " + name)
	}
	commands[name] = command{name: name, summary: summary, run: run}
}

func init() {
	register("help", "list commands, or describe one: help <command>", nil)
	register("version", "print the version: version (also --version)", nil)
}

// version is set at build time: go build -ldflags "-X main.version=v1.2.3".
var version = "dev"

// credSource names where the credential came from, for the 401 message. It is
// a label such as GRAPH_API_KEY, never the secret.
var credSource = "unknown"

func sortedCommands() []command {
	out := make([]command, 0, len(commands))
	for _, c := range commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// dispatch runs args[0] when it is a registered command. handled is false when
// it is not, so main falls through to the bare-flag watch/demo path.
func dispatch(args []string, stdout, stderr io.Writer) (code int, handled bool) {
	if len(args) == 0 {
		return 0, false
	}
	if args[0] == "--version" || args[0] == "-version" {
		fmt.Fprintln(stdout, "graphwatch", version)
		return exitOK, true
	}
	cmd, ok := commands[args[0]]
	if !ok {
		if strings.HasPrefix(args[0], "-") {
			return 0, false // a watch flag: the default command
		}
		fmt.Fprintf(stderr, "graphwatch: unknown command %q\n\n", args[0])
		runHelp(nil, stderr, stderr)
		return exitUsage, true
	}
	if cmd.name == "version" {
		fmt.Fprintln(stdout, "graphwatch", version)
		return exitOK, true
	}
	if cmd.name == "help" {
		return runHelp(args[1:], stdout, stderr), true
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runCommand(ctx, cmd, args[1:], stdout, stderr), true
}

// globalFlags registers the flags every command parses.
type globalFlags struct {
	base    *string
	json    *bool
	limit   *int
	noColor *bool
	keyFile *string
	timeout *time.Duration
}

func newGlobalFlags(fs *flag.FlagSet) globalFlags {
	return globalFlags{
		base:    fs.String("base", "", "api origin (default: GRAPH_BASE_URL, the plugin's saved base_url, then https://api.instruxi.dev)"),
		keyFile: fs.String("api-key-file", "", "read the API key from this file (first in the credential order)"),
		json:    fs.Bool("json", false, "print the API's data payload as JSON"),
		limit:   fs.Int("limit", 0, "show at most this many rows (0: all)"),
		noColor: fs.Bool("no-color", false, "no colour (NO_COLOR is also respected)"),
		timeout: fs.Duration("timeout", defaultTimeout, "per-request timeout (a stalled connection is reported after this long)"),
	}
}

// flagSet reports whether a bool flag declared through commandFlags was set on
// the shared flag set (before the positional arguments).
func flagSet(env *cliEnv, name string) bool {
	f := env.fs.Lookup(name)
	return f != nil && f.Value.String() == "true"
}

// flagsFirst moves every flag the shared flag set knows to the front of args,
// so `attend g1 --poll 1` parses like `attend --poll 1 g1`: Go's flag package
// stops at the first positional word. Anything else (positionals, and flags a
// command parses itself, such as inbox's --watch) keeps its order after them,
// and a bare -- ends the search.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		name, _, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		f := fs.Lookup(name)
		if !strings.HasPrefix(a, "-") || a == "-" || f == nil {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); !hasVal && !(ok && bf.IsBoolFlag()) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	// The leading -- keeps a positional that looks like a flag a positional.
	return append(append(flags, "--"), rest...)
}

func runCommand(ctx context.Context, cmd command, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("graphwatch "+cmd.name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	g := newGlobalFlags(fs)
	if setup := commandFlags[cmd.name]; setup != nil {
		setup(fs)
	}
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *g.timeout <= 0 {
		fmt.Fprintln(stderr, "graphwatch: --timeout must be positive")
		return exitUsage
	}
	if *g.limit < 0 {
		fmt.Fprintln(stderr, "graphwatch: --limit must be 0 or more")
		return exitUsage
	}
	cred, source, base, err := resolveFromProcess(*g.keyFile, *g.base)
	if err != nil {
		fmt.Fprintln(stderr, strings.TrimPrefix(err.Error(), "graphwatch: "))
		return exitUsage
	}
	credSource = source
	cl := newClient(base, cred)
	cl.http.Timeout, cl.slowNote = *g.timeout, stderr
	env := &cliEnv{client: cl, base: base, source: source, out: stdout, errOut: stderr,
		json: *g.json, limit: *g.limit, noColor: *g.noColor, isTTY: isTerminal(stdout), fs: fs}
	return cmd.run(ctx, env, fs.Args())
}

func runHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		cmd, ok := commands[args[0]]
		if !ok {
			fmt.Fprintf(stderr, "graphwatch: unknown command %q\n", args[0])
			return exitUsage
		}
		fmt.Fprintf(stdout, "usage: graphwatch %s [flags]\n\n%s\n\n", cmd.name, cmd.summary)
		fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
		fs.SetOutput(stdout)
		newGlobalFlags(fs)
		if setup := commandFlags[cmd.name]; setup != nil {
			setup(fs)
		}
		fmt.Fprintln(stdout, "flags:")
		fs.PrintDefaults()
		if cmd.name == "resolve" {
			fmt.Fprint(stdout, resolveHelp)
		}
		if notes := commandNotes[cmd.name]; notes != "" {
			fmt.Fprintf(stdout, "\n%s command flags (read from the arguments after the graph):\n%s", cmd.name, notes)
		}
		return exitOK
	}
	fmt.Fprintln(stdout, "usage: graphwatch <command> [flags]")
	fmt.Fprintln(stdout, "       graphwatch --graph <id> | --demo   (watch; the default command)")
	fmt.Fprintln(stdout, "\ncommands:")
	for _, c := range sortedCommands() {
		fmt.Fprintf(stdout, "  %-12s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(stdout, "\nglobal flags: --base <url>  --json  --limit <n>  --no-color  --api-key-file <path>  --timeout <duration>")
	return exitOK
}

// reportError prints an API failure as `graphwatch: <status> <code>: <message>`
// from the API's error envelope. Only the response is used, never the request,
// so no header value can leak. It returns the exit code.
func reportError(w io.Writer, err error) int {
	var ae *apiError
	if errors.As(err, &ae) && ae.status == 401 {
		fmt.Fprintf(w, "graphwatch: signed out or key rejected (credential source: %s): %s\n", credSource, signedOutFix(credSource))
		return exitRuntime
	}
	if errors.As(err, &ae) && ae.status != 0 {
		code, msg := ae.code, ae.detail
		if code == "" {
			code = "error"
		}
		if msg == "" {
			msg = ae.msg
		}
		fmt.Fprintf(w, "graphwatch: %d %s: %s\n", ae.status, code, msg)
		return exitRuntime
	}
	// status 0 is a transport failure (refused, timed out, no route): say what
	// went wrong in words before the detail.
	if errors.As(err, &ae) && ae.status == 0 {
		fmt.Fprintf(w, "graphwatch: could not reach the API: %s\n", ae.msg)
		return exitRuntime
	}
	fmt.Fprintln(w, "graphwatch:", err)
	return exitRuntime
}

// parseErrorEnvelope pulls the machine code and message out of an error body:
// {"error":{"code","message"}}, {"code","message"} or {"error":"text"}.
func parseErrorEnvelope(b []byte) (code, msg string) {
	var raw struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return "", ""
	}
	code, msg = raw.Code, raw.Message
	if len(raw.Error) > 0 {
		var o struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		var s string
		if json.Unmarshal(raw.Error, &o) == nil && (o.Code != "" || o.Message != "") {
			code, msg = o.Code, o.Message
		} else if json.Unmarshal(raw.Error, &s) == nil && msg == "" {
			msg = s
		}
	}
	return strings.TrimSpace(code), strings.TrimSpace(msg)
}

// resolveFromProcess is the one place a command, the watch path and --demo get
// their credential: resolveCredential over the real environment. Its error
// already carries the "graphwatch:" prefix and the order it looked in.
func resolveFromProcess(keyFile, base string) (credential, string, string, error) {
	home, _ := os.UserHomeDir()
	cred, source, b, err := resolveCredential(os.LookupEnv, home, authFlags{apiKeyFile: keyFile, base: base})
	if err != nil && !strings.HasPrefix(err.Error(), "graphwatch:") {
		err = fmt.Errorf("graphwatch: %w", err)
	}
	return cred, source, b, err
}
