# AGENTS.md: working in graphwatch

One page. Read it once, then work.

## What this repository is

graphwatch, a command-line client for a plan held in Enforcer Graph, extracted
from the service's own tree so it can be read, run and changed without the
service, plus its demos:

- the repository root: `graphwatch`, a Go module (`github.com/instruxi-io/graphwatch`).
  Standard library only, on purpose. `go run . --demo --layout mycelium` is an
  offline demo that needs no API and no key; `--graph <id>` watches a real
  graph; `--demo` alone creates and works one in your workspace.
- `demos/graph-live/`: a browser viewer in React. Watch mode reads and listens and
  never writes. Demo mode creates a `demo-<scenario>-<time>` graph and drives
  it through the API as a set of harnesses would.

Both draw the same thing: nodes as dots coloured by the server's `work_state`,
edges flipped so work flows down and out, heat for activity.

## Run and verify

```bash
go build ./... && go vet ./... && go test ./...
cd demos/graph-live && GITHUB_TOKEN=$(gh auth token) npm ci && npm run typecheck && npm run build
```

`npm run build` needs no API key. `npm run dev` refuses to start without
`GRAPH_API_KEY`, by design: the Vite dev server proxies `/api/v1/graph` to the
real API and adds the key there, so the key never reaches the bundle or the
browser. Do not move the key into client code.

The golden tests at the root compare the layered layout (four frames)
and the mycelium demo frames (mid-growth and settled, particles removed) against
text files in `testdata/`. Growth is seeded from node ids with no clock and no
randomness; if you change what is drawn, regenerate the goldens on purpose
(`go test ./... -run GoldenPinned -update`)
and say so in the commit.

## Rules that are not obvious

1. **A `requires` edge points FROM the dependent TO its prerequisite.** Both
   viewers flip it once, in one place (`layout.go`,
   `demos/graph-live/src/live.ts`). Flip it anywhere else and arrows reverse silently.
2. **Draw the server's `work_state`; never re-derive it** from status and edges.
   The service computes it in one SQL function and the viewers would disagree
   with it in exactly the cases that matter (reclaims, verifying, held).
3. **Every event means refetch.** The stream is a clock, not a data source. Its
   frames are thin; the viewers invalidate and re-read `/nodes` and `/edges`.
4. **Watch mode writes nothing.** The demo driver is the only code that calls a
   write, and only Demo mode calls into it. Keep it that way, and keep
   `GRAPH_LIVE_LOG=1` useful: it prints method and path per proxied request so a
   reviewer can confirm Watch mode issues only GETs and the stream.
5. **Nothing is archived automatically.** A demo graph stays until a person
   archives it. Do not add cleanup that deletes graphs.
6. **No hand-written fetch in graph-live.** Reads and the driver's writes go
   through the generated `@instruxi-io/graph-hooks` functions. That is the point
   of the example. `graphwatch` is the opposite: a plain `net/http` client,
   because it must build with the standard library alone.
7. **Send only changed cells.** `graphwatch` diffs frames and quantises colours
   to 16 levels per channel so a cooling node stops repainting. A full repaint
   per frame was measured growing a terminal's memory by about 0.3 GB a minute.
8. **Never commit a key, a token or a transcript.** `.npmrc` reads `GITHUB_TOKEN`
   from the environment at install time and is committed without a value.
9. **Navigation commands are read-only.** Every `graphwatch` command other than
   `--demo`, `decide`, `resolve` and `attend` issues GET only (`decide` and
   `resolve` record an answer with one POST, and `attend` posts the same two
   when you type an answer; none writes unless you run it that way); each lives in `cmd_<name>.go` and
   registers itself from `init()`.

## Credentials

- `GRAPH_API_KEY`: an Enforcer API key. Both viewers accept it.
- `GRAPH_AUTH_HELPER`: `graphwatch` only: a command that prints a JSON object
  of request headers. Pointing it at the `enforcer` Claude Code plugin's header
  helper lets the viewer ride the plugin's OAuth sign-in and refresh on a 401.
- `GRAPH_BASE_URL`: the API origin, default `https://api.instruxi.dev`.

A key or sign-in with only `enforcer:read` is enough to watch. Demo mode also
needs `enforcer:graph-graph-import.write` (graphwatch creates its demo graph
through `POST /graphs/import`) or the graph-nodes, graph-edges and graph-runs
write scopes (graph-live's driver claims, heartbeats and completes runs).

## Conventions

- Keep each viewer self-contained. `graphwatch` imports nothing outside the
  standard library; `graph-live` depends on React, TanStack Query, xyflow and
  the published hooks, nothing else.
- Comments say why, not what. The existing files are the register to match.
- A change to what is drawn gets a golden test or a screenshot in the PR.
- Scenarios live in `demos/graph-live/src/scenarios/<name>.ts` with an id, title,
  one-line description, `build()` and `drive()`. Add one by adding a file and
  registering it in `index.ts`.

## Where the rest is

- The `enforcer` Claude Code plugin, install and sign-in:
  https://github.com/instruxi-io/agent-plugins
- The graph service and the hook packages are private to the organisation. The
  two conventions that matter here: a `requires` edge points from the dependent
  to its prerequisite, and a loop is a set of runs, never an edge.
