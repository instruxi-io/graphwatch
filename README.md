# graphwatch

A command-line client for a plan held in [Enforcer Graph](https://gvnr.io/graphwatch):
a tenant-scoped DAG service where agents claim nodes, do the work, and have each
completion judged against acceptance criteria before anything downstream
unblocks. It draws a graph live in the terminal, lists and queries its nodes,
runs, reviews and memory, and lets a person answer the gates and review items
that need them. Go, standard library only.

```bash
go run . --demo --layout mycelium      # offline: no API, no key
go run . --graph <graph-id>            # watch a real graph
go run . --demo                        # create a demo graph in your workspace and work it
```

## Install

```bash
go install github.com/instruxi-io/graphwatch@latest
```

Or without Go: every version tag publishes a static binary for Linux, macOS and
Windows on amd64 and arm64, with a `SHA256SUMS` file, on the
[releases page](https://github.com/instruxi-io/graphwatch/releases).

```bash
# macOS on Apple silicon; swap the suffix for linux-amd64, linux-arm64, darwin-amd64
curl -fsSLO https://github.com/instruxi-io/graphwatch/releases/latest/download/graphwatch-darwin-arm64
curl -fsSLO https://github.com/instruxi-io/graphwatch/releases/latest/download/SHA256SUMS
grep graphwatch-darwin-arm64 SHA256SUMS | shasum -a 256 -c
chmod +x graphwatch-darwin-arm64 && mv graphwatch-darwin-arm64 /usr/local/bin/graphwatch
graphwatch --demo --layout mycelium
```

On macOS a downloaded binary is quarantined; clear it with
`xattr -d com.apple.quarantine /usr/local/bin/graphwatch` if Gatekeeper refuses it.

Every release binary also carries a signed build provenance attestation, so you
can check it was built from this repository by its release workflow:

```bash
gh attestation verify graphwatch-darwin-arm64 --repo instruxi-io/graphwatch
```

### Platforms

| platform | support |
|---|---|
| Linux, macOS (amd64, arm64) | everything |
| Windows under WSL | everything |
| Windows, native | every table command and `--json`; the watch view draws at a fixed 100x32 and cannot read keys; the picker needs WSL. Use Windows Terminal for colour |

## Enterprise use

- **Network.** graphwatch talks only to the API origin (`--base`, then
  `GRAPH_BASE_URL`, then the `enforcer` plugin's saved `base_url`, then
  `https://api.instruxi.dev`). It has no telemetry and no update check.
- **Proxies and CAs.** It uses Go's standard HTTP client, so `HTTPS_PROXY`,
  `HTTP_PROXY` and `NO_PROXY` apply, and TLS trusts the system certificate
  store (on Linux, `SSL_CERT_FILE` or `SSL_CERT_DIR` add a corporate CA).
- **Credentials.** Prefer a sign-in through the `enforcer` plugin or
  `GRAPH_AUTH_HELPER` (short-lived OAuth) over a long-lived `GRAPH_API_KEY`;
  the full order is under [Auth](#auth). A key or sign-in with only
  `enforcer:read` is enough to watch and list.
- **Writes.** Every command is read-only except `decide`, `resolve`, `attend`
  (only when you type an answer) and `--demo`. The service still checks each
  write against your role.
- **Supply chain.** One static binary with no dependencies outside the Go
  standard library, built by GitHub Actions with provenance attestations,
  checked by `govulncheck` in CI and before every release.
- **Vulnerabilities.** See [SECURITY.md](./SECURITY.md).

## Demos

| demo | what it is | needs an account? |
|---|---|---|
| `graphwatch --demo --layout mycelium` | A three-root, depth-four plan worked in-process and drawn as hyphae growing from the roots to the leaves. Nothing leaves your machine. | No |
| `graphwatch --demo` | Creates a `demo` graph in your workspace, grows it and works it with simulated harnesses while you watch. Nothing is archived automatically. | Yes |
| [`demos/graph-live/`](./demos/graph-live) | A browser viewer: the same picture in React, plus scenarios that create a graph and work it the way a set of harnesses would. The minimal reference for `@instruxi-io/graph-hooks`, which is distributed to Instruxi organisation members only. | Yes |

## What it draws

- **layers** (default): prerequisites sit above what they unblock, ordered by a
  Sugiyama barycenter pass. Nodes are dots shaded by `work_state`. Work shows as
  heat: a pulse travels down an edge into a node when its run starts and out
  when it finishes, and everything cools when the plan goes quiet.
- **mycelium** (`--layout mycelium`): a radial view with the roots on an inner
  ring and depth as radius. Hyphae grow from prerequisite to dependent with a
  neighbour-sensing model (steered tips with persistence, attraction to
  targets, negative autotropism via an occupancy field, lateral branches 30 to
  70 degrees off), and fuse on contact. Growth is seeded from node ids and
  cached; a frame only chooses how much to reveal and in what colour.

Rendering is a Braille sub-pixel canvas (2 by 4 dots per cell) under a text
layer. Colours are 24-bit, quantised to 16 levels per channel, and only changed
cells are written each frame.

## What you are looking at

- A **dot** is a node. Its colour is the server's `work_state`: looking for work,
  claimed, looking for validation or arbitration, waiting, held for review, done.
- An **edge** points from a dependent to its prerequisite in the API; both viewers
  flip it so work flows downward and outward on screen.
- **Heat** is activity: a pulse travels into a node when a run starts and out when
  it finishes. A quiet plan stops drawing.
- A node is **done** only after its run was judged against the acceptance
  criteria frozen at claim time. A rejected run sends the node back to the
  frontier with its dependents still blocked.

## Flags

| flag | default | meaning |
|---|---|---|
| `--graph` | | graph id to watch |
| `--demo` | false | create a graph, grow it and work it while watching; with `--layout mycelium` an offline demo that needs no API |
| `--size` | 36 | demo: nodes to plan |
| `--workers` | 4 | demo: concurrent harnesses |
| `--fail` | 0.08 | demo: chance a run fails and is retried |
| `--stay` | false | keep watching after the plan completes |
| `--fps` | 15 | frames per second (only changed cells are sent) |
| `--motion` | events | `events`: motion follows graph events and a quiet graph stops drawing; `continuous`: running nodes pulse |
| `--layout` | layers | `layers` or `mycelium` |
| `--no-overlays` | false | plain header: drop the epoch, work_state counts, review holds, recruiting demand and stream cursor |
| `--base` | `$GRAPH_BASE_URL` or `https://api.instruxi.dev` | API origin |
| `--api-key-file` | | read the API key from this file |
| `--timeout` | 10s | per-request timeout; a request still waiting after 3 s prints `connecting to <host>...` to stderr |
| `--no-color` | false | accepted, but the watch view needs colour: it prints a one-line message and exits 2 (so does `NO_COLOR`) |
| `--json`, `--limit` | | table commands only; the watch view rejects them |

`inbox` also takes two flags of its own, after the graph argument:

| flag | meaning |
|---|---|
| `--watch` | `inbox`: re-read every N seconds (1 or more) and print only changes, `+` for a new item and `-` for one that is gone; cannot be combined with `--json` |
| `--kind` | `inbox`: `review` or `gate` lists only that kind; any other value exits 2 |

The watch view draws on a terminal of at least 80x24. On a smaller one, with
stdout redirected, with `NO_COLOR` set or with `TERM=dumb` it prints one line to
stderr and exits 2 instead of drawing. `graphwatch --version` (or `graphwatch
version`) prints the version; a release build sets it with
`-ldflags "-X main.version=<version>"`, and a plain build prints `dev`. An
unknown first word, such as `graphwatch bogus`, prints the command list and
exits 2.

## Commands

`graphwatch` with no arguments opens the picker; `graphwatch <command>` runs one
of the commands below. All of them are read-only (GET only) except `decide`, `resolve` and `attend`, which write only when you answer: one POST per answer. Global flags:
`--base <url>  --json  --limit <n>  --no-color  --api-key-file <path>  --timeout <duration>`. Flags may come before or after the
positional arguments (`graphwatch nodes --json <graph>` and `graphwatch nodes <graph> --json` are the same).
`graphwatch help <command>` lists a command's flags.
Text that comes from the API (titles, reasons, reports, evidence) is printed with
control characters shown as escapes such as `\x1b`, so a hostile title cannot
move the cursor or clear your screen; `--json` output is untouched.

| command | what it does | worked example |
|---|---|---|
| `graphwatch whoami` | the account, tenant and role the API resolves, and where the credential came from | `graphwatch whoami` |
| `graphwatch graphs` | list the graphs you can see (`--archived` includes archived ones, `--full-ids` prints whole ids), or `graphs show <id\|slug>` for one with its roll-up | `graphwatch graphs` |
| `graphwatch nodes` | list a graph's tasks (`--status`, `--type`), or `nodes show <graph> <key\|id>` with its runs | `graphwatch nodes --status active <graph>` |
| `graphwatch edges` | list a graph's dependencies; `--as-flow` prints prerequisite to dependent | `graphwatch edges --as-flow <graph>` |
| `graphwatch runs` | a node's runs and how they were judged; `runs show <graph> <node> <run>` (node by key or id, run by id or unique prefix; `--full` prints evidence in full) | `graphwatch runs <graph> <node>` |
| `graphwatch review` | human review items holding a graph (`--open`, `--all`), then the gates awaiting a decision; `review show <graph> <item>` | `graphwatch review <graph>` |
| `graphwatch decide` | record an arbitrator's decision on a gate node (`--note`, `--follow-up`, repeatable); prints the node's new status and the nodes it released | `graphwatch decide <graph> ship-gate ship --note "smoke passed"` |
| `graphwatch inbox` | one numbered list of what needs a person: open review items (kind, item, node, age, vote tally and weakest criterion when the item carries them) and gates whose prerequisites are done; prints `nothing needs you` when empty | `graphwatch inbox <graph> [--watch secs] [--kind review|gate]` |
| `graphwatch resolve` | answer a review item with `approve`, `reject` or `override` (`--note`, required for override); prints the node's new status and its dependents. Needs arbitrator rights; the service refuses anyone else | `graphwatch resolve <graph> run:3f9a approve --note "checked"` |
| `graphwatch attend` | follow a graph and answer what needs you inline (see Attended mode); `--poll` sets the re-read interval in seconds, default 15 | `graphwatch attend <graph>` |
| `graphwatch epochs` | replay history; `epochs show <graph> <epoch>` | `graphwatch epochs <graph>` |
| `graphwatch access` | who can reach a graph (`--full-ids`) | `graphwatch access <graph>` |
| `graphwatch recruiting` | what one graph recruits for | `graphwatch recruiting <graph>` |
| `graphwatch work` | the work lobby: graphs looking for workers or validation (`--state`, `--kind`, `--tier`, `--for`, `--graph`, `--watch`, seconds between polls, minimum 5) | `graphwatch work --kind work` |
| `graphwatch query` | query nodes or runs across every graph you can see; `--all` follows `next` | `graphwatch query nodes --all` |
| `graphwatch recall` | search a graph's memory (its observations): `--q` text, `--node` key or id, `--file` path, `--kind`, `--since` (a positive window such as `30m`, `2h` or `3d`, applied to created_at; zero or negative exits 2; not applied with `--json`), `--sort` (`rank` or `time`; rank keeps the server's order, time is newest first; not applied with `--json`). `--since` and `--sort time` read the result set 50 hits a page, up to 500 hits, then filter, sort and cut to `--limit`; if the 500 bound is reached a note on stderr says so, and the global `--limit` (at most 50 for recall, otherwise exit 2); prints kind, node, created_at, run id and the first line of each body, `--json` for the raw payload | `graphwatch recall --q sqlite --limit 5 <graph>` |
| `graphwatch context` | a graph's context pack: sha256, size, build time and whether recall is enabled | `graphwatch context <graph>` |
| `graphwatch tail` | follow a graph's event stream as text; `--cursor` resumes from a cursor, `--save-cursor` resumes next run | `graphwatch tail --save-cursor <graph>` |
| `graphwatch help` | list commands, or describe one | `graphwatch help runs` |

Output below is copied from a real run against a local fake API (ids shortened,
no credentials). Real output depends on your graph.

```text
$ graphwatch graphs
ID        SLUG   NAME        MODE  LIFECYCLE
11111111  alpha  Alpha plan  dag   open

$ graphwatch nodes <graph>
KEY     TYPE  STATUS  WORK_STATE        TIER  TITLE
design  task  done    finished          -     Design
build   task  active  looking_for_work  -     Build

$ graphwatch nodes --status active <graph>
KEY    TYPE  STATUS  WORK_STATE        TIER  TITLE
build  task  active  looking_for_work  -     Build

$ graphwatch edges <graph>
FROM_KEY  RELATION    TO_KEY  TYPE
build     depends on  design  requires

$ graphwatch edges --as-flow <graph>
PREREQUISITE  ->  DEPENDENT  TYPE
design        ->  build      requires
```

The `requires` edge points from the dependent to its prerequisite; `edges`
labels it "depends on", and `--as-flow` flips it for reading.

`runs`, `review`, `epochs`, `access`, `recruiting`, `work`, `query` and `tail`
follow the same pattern: `graphwatch help <command>` gives the exact flags, and
each prints a table and accepts `--json`.

### `query` flags

Each filter is sent to the server as the query parameter of the same name
(dashes for underscores); the server rejects one it does not know for that
kind. `graphwatch help query` gives each flag's meaning.

| flag | filters |
|---|---|
| `--graph-id` | only this graph (id) |
| `--status` | node or run status, e.g. active or done |
| `--type` | node type |
| `--key` | node key (exact match) |
| `--q` | free-text search over node titles and keys |
| `--epoch` | only this epoch number |
| `--assignee` | nodes assigned to this account |
| `--assignee-group` | nodes assigned to this group |
| `--claimer` | runs claimed by this account |
| `--runner` | runs recorded under this runner label |
| `--verdict` | runs with this verification verdict (e.g. verified, rejected) |
| `--waiting` | nodes waiting on this condition (e.g. judges or a human) |
| `--link-system` | nodes linked to this external system |
| `--link-role` | nodes whose link has this role |
| `--has-output` | nodes that declare or hold this output name |
| `--from` | only rows at or after this time (RFC 3339) |
| `--to` | only rows before this time (RFC 3339) |
| `--time-field` | which timestamp --from and --to apply to |
| `--include-archived` | true to include archived graphs |
| `--work-state` | the server's work_state, e.g. looking_for_work |
| `--sort` | sort field, with a leading minus for descending |
| `--group-by` | group rows by this field and print counts instead of rows |
| `--agg` | aggregate to compute per group, e.g. count or avg:field |
| `--tz` | time zone for time bucketing, e.g. UTC |
| `--offset` | skip this many rows (grouped results page by offset) |
| `--after` | resume after this page cursor |
| `--node-id` | runs of this node (id) |
| `--node-key` | runs of the node with this key |
| `--node-type` | runs of nodes of this type |
| `--attempt` | runs with this attempt number |
| `--verdict-resolution` | how the verdict was resolved, e.g. judge, vote or human |
| `--min-duration-seconds` | runs that took at least this many seconds |
| `--max-duration-seconds` | runs that took at most this many seconds |
| `--has-evidence` | true for runs that carry evidence |
| `--where` | predicate `path:op:value`, repeatable and ANDed |
| `--all` | follow the next-page cursor until exhausted, bounded by `--limit` (default 200) |

### `--json` piping

`--json` prints the API's data payload, so it pipes into `jq`:

```bash
graphwatch nodes --json <graph> | jq -r '.[].key'
graphwatch nodes --json <graph> | jq '[.[] | select(.work_state == "looking_for_work")] | length'
graphwatch graphs --json | jq -r '.[].slug'
```

```text
$ graphwatch nodes --json <graph> | jq -r '.[].key'
design
build
```

## Attended mode

`graphwatch attend <graph>` prints the inbox (the same listing as `inbox`),
then follows the graph's event stream and prints each new item as it appears,
with a terminal bell (`\a`) and a status line such as `2 need you`. Any stream
event triggers a re-read of the review list and the nodes. If the stream is
refused or drops, it re-reads every `--poll` seconds (default 15) instead. A
gate counts as ready when its prerequisites are all done.

Input is one line at a time on stdin, so it needs no terminal mode and works the
same on every platform:

```
<n> approve|reject|override [note...]   answer review item n (override needs a note)
<n> decide <decision> [note...]         decide gate n
list    help    quit
```

Item numbers are fixed when an item first appears and are not reused. If a gate
lists `options`, a decision outside them is refused before anything is sent. An
answer is sent under your own credential, so the service still decides whether
you may: a 403 prints `you are not an arbitrator on this graph`. At end of
input `attend` exits 0, so it can be piped.

## Interactive picker

`graphwatch` with no arguments lists your graphs.

| key | action |
|---|---|
| `up` / `k`, `down` / `j` | move the cursor |
| `/` | filter the list (Esc clears the filter) |
| `enter` | watch the graph (layers) |
| `m` | watch the graph as mycelium |
| `t` | tail the graph's events |
| `r` | refresh the list |
| `q` / `esc` | quit |

## Watch view: inspector and header

In the watch view the arrow keys (or `h j k l`) move a selection to the nearest
node, `enter` opens the inspector (the node's latest run, evidence, verdicts,
tier and route) and `esc` closes it. While it is open, moving the selection
shows the next node, and an event for that node refetches it.

The header shows the epoch, work_state counts, review holds, recruiting demand
and the stream cursor. `--no-overlays` drops it back to a plain header.

## Auth

Every command and the watch view use one resolution order, from `auth.go`. The
first source that is set wins:

1. `--api-key-file <path>`: the key is read from that file
2. `GRAPH_AUTH_HELPER`: a command that prints a JSON object of request headers; run again on a 401
3. `GRAPH_API_KEY`
4. `ENFORCER_API_KEY`
5. the `enforcer` Claude Code plugin's sign-in, through its header helper
6. `enforcer.api_key` in `~/.enforcer/credentials.json`

The API origin is `--base`, then `GRAPH_BASE_URL`, then the plugin's saved
`base_url`, then `https://api.instruxi.dev`.

graphwatch will never refresh OAuth itself: the plugin's helper rotates a
single-use refresh token and writes it back, so refreshing here, or writing
`credentials.json`, would sign the plugin out. Sign in with `/enforcer:login`
in Claude Code. No command prints a key, token or Authorization header.

`graphwatch whoami` shows which source won:

```text
$ graphwatch whoami
FIELD       VALUE
base url    http://127.0.0.1:8765/api/v1/graph
credential  GRAPH_API_KEY
account     dev@example.com
tenant      Example Workspace
role        admin
```

The live `--demo` creates its graph through `POST /graphs/import`, so a sign-in
holding `enforcer:graph-graph-import.write` is enough; it does not need
`graph-graphs.write`.

## Tests

`go test ./...` runs unit tests and golden tests. The golden files are plain
text under `testdata/` and are compared in full; a failure prints the
first differing lines.

- `layers_frames.txt`: four frames (the first, the two around a status change,
  and the last of 90) of the layered layout.
- `mycelium_mid.txt`, `mycelium_settled.txt`: the fixed demo plan at 100x36,
  mid-growth and settled, with the travelling particles removed because their
  speeds vary from run to run. Growth itself uses no clock and no randomness.

A changed golden means a changed drawing. If that is intended, regenerate on
purpose and review the diff:

    go test ./... -run GoldenPinned -update

## Licence and scope

graphwatch and the demos in this repository are MIT licensed (see
[LICENSE](./LICENSE)). The Enforcer Graph service and its API are not part of
this licence and are not open source; using graphwatch against a real plan
requires an Enforcer account. See [AGENTS.md](./AGENTS.md) before changing
anything.
