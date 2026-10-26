package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	register("tail", "follow a graph's event stream as text; --save-cursor resumes next run", runTail)
	commandFlags["tail"] = func(fs *flag.FlagSet) {
		fs.String("cursor", "", "resume from this cursor (sent as Last-Event-ID)")
		fs.Bool("save-cursor", false, "store the last cursor under the user cache dir and resume from it next run")
	}
}

func cursorPath(graph string) (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "graphwatch", filepath.Base(graph)+".cursor"), nil
}

func saveCursor(graph, cur string) error {
	p, err := cursorPath(graph)
	if err != nil || cur == "" {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(cur+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}

func loadCursor(graph string) string {
	p, err := cursorPath(graph)
	if err != nil {
		return ""
	}
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func runTail(ctx context.Context, env *cliEnv, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(env.errOut, "usage: graphwatch tail [--json] [--cursor <id>] [--save-cursor] <graph>")
		return exitUsage
	}
	graph := args[0]
	cursor := env.fs.Lookup("cursor").Value.String()
	save := env.fs.Lookup("save-cursor").Value.String() == "true"
	if cursor == "" && save {
		cursor = loadCursor(graph)
	}
	// One cached read resolves node ids to keys; a failure only means ids print raw.
	keys := map[string]string{}
	if ns, err := env.client.nodes(ctx, graph); err == nil {
		for _, n := range ns {
			keys[n.ID] = n.Key
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	last, code := cursor, exitOK
	finish := func() {
		if save && last != "" {
			if err := saveCursor(graph, last); err != nil {
				fmt.Fprintln(env.errOut, "graphwatch: cannot save cursor:", err)
			}
		}
	}
	env.client.streamFrom(ctx, graph, cursor, func(ev sseEvent) {
		if ev.ID != "" {
			last = ev.ID
		}
		switch ev.Event {
		case ":", "ready":
			return
		case "closed":
			cancel()
			return
		case "reset":
			fmt.Fprintln(env.out, "RESET: cursor too old, replaying from live")
			return
		}
		if len(ev.Data) == 0 {
			return
		}
		if env.json {
			fmt.Fprintln(env.out, string(ev.Data))
		} else {
			fmt.Fprintln(env.out, tailLine(ev.Data, keys))
		}
		if save {
			finish()
		}
	}, func(s string) {
		fmt.Fprintln(env.errOut, "graphwatch:", s)
		if strings.HasPrefix(s, "refused") {
			code = exitRuntime
		}
	})
	finish()
	return code
}

func tailLine(data json.RawMessage, keys map[string]string) string {
	var e struct {
		Verb         string         `json:"verb"`
		ResourceType string         `json:"resource_type"`
		ResourceID   *string        `json:"resource_id"`
		Actor        *string        `json:"actor_account_id"`
		Metadata     map[string]any `json:"metadata"`
		CreatedAt    time.Time      `json:"created_at"`
	}
	if json.Unmarshal(data, &e) != nil {
		return clean(string(data))
	}
	res, tr, actor := "-", "", "-"
	if e.ResourceID != nil {
		res = *e.ResourceID
		if k, ok := keys[res]; ok {
			res = k
		}
	}
	if e.Metadata["from"] != nil || e.Metadata["to"] != nil {
		tr = fmt.Sprintf(" %v->%v", e.Metadata["from"], e.Metadata["to"])
	}
	if e.Actor != nil {
		actor = *e.Actor
	}
	ts := e.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	return fmt.Sprintf("%s %s %s%s %s", ts.Local().Format("15:04:05"), clean(e.Verb), clean(res), clean(tr), clean(actor))
}
