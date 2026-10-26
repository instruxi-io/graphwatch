# graph live

A React page that shows a graph being worked, live. It's the minimal
reference for **the published hooks plus the stream**:
**`@instruxi-io/graph-hooks`** (0.1.266) supplies the data and
**`useGraphStream`** keeps all of it current. The app contains no hand-written
fetch or SSE code. Reads go through generated hooks and functions
(`useGetGraph`, `useGetGraphSummary`, `queryNodes`, `listEdges`,
`useListGraphs`, and for the drawer `useGetNode`, `useListNodeRuns` and
`useListNodeObservations`). The demo driver's writes go through generated
functions too. It's the browser sibling of [`graphwatch`](../../) (the CLI at this repository's root). It stays small
on purpose: it draws the graph and drives demos, with no other panels.

```bash
export GRAPH_API_KEY=<your Enforcer API key>
GITHUB_TOKEN=$(gh auth token) npm install   # GitHub Packages needs a token to read
npm run dev                                  # http://127.0.0.1:5178
```

If startup fails with `ENOSPC: System limit for number of file watchers
reached`, either raise the limit
(`sudo sysctl fs.inotify.max_user_watches=524288`) or run
`GRAPH_LIVE_POLL=1 npm run dev`, which polls for changes instead of using
inotify. `npm run build` needs no key.

## Two modes

The header has two tabs, and they are kept apart on purpose.

- **watch** reads and listens, and never writes. Open the **graph ▾** picker,
  search by name, and pick a graph. Whoever is working it (harnesses, people,
  a demo in another tab) keeps working it, and the picture follows. **hide demo
  graphs** in the picker filters out anything named `demo-…` (or `graph-live-…`,
  from older plays). The header shows `read-only`. Nothing Watch loads can
  write: the demo driver is the only code that calls a write, and only Demo mode
  calls into it.
- **demo** creates a new graph for a scenario and drives it through the API,
  as a set of harnesses would. Pick a scenario and a speed, then press **▶ run**.
  **■ stop** aborts it: workers stop claiming, and any run still held is
  reported `failed` ("demo stopped"). That leaves the node retryable, not
  finished. The graph stays where it is after a run or a stop. **watch it**
  opens it in Watch mode, and **archive** archives it (`DELETE /graphs/{id}`).
  Nothing is archived automatically. Switching to Watch stops any demo that's
  running, so no player is ever left running in the background.

### Scenarios

Each scenario lives in `src/scenarios/<name>.ts`: an id, a title, a one-line
description, `build()` (which creates the graph) and `drive()` (which works it
with the primitives in `src/demo/driver.ts`). Every graph a scenario creates
has the slug `demo-<scenario>-<unix time>`.

| `?demo=` | what it shows |
|---|---|
| `plan-100` | The original ▶ play. A planner grows a plan in bursts (100 nodes, or `?play=N`) while six workers claim and complete it. About 7% of runs fail and are retried. |
| `recipe` | A cassoulet. Four branches (confit, beans, pork, sausage) join at assembly, followed by three bakes in sequence. Three cooks work it, at a pace you can follow. |
| `ci-pipeline` | Lint and test fan out per package, then build, images, staging, smoke, production. `test_web` and `lint_sdk` fail their first attempt and retry. |
| `fan-in` | One split, 24 parallel shards and a single merge. One slow shard is the critical path, and the header's `critical path` count (from the summary) shrinks as it runs. |
| `failures-and-reclaims` | Three workers crash while holding a claim. Their nodes stay `claimed` until the 30 s lease lapses (the API minimum, in real time whatever the speed), then turn **reclaimable** and are taken over. Two long jobs heartbeat to keep their lease past 30 s. |
| `validation` | Every completion is judged by Jev, as a 1-of-1 quorum. Nodes sit in *looking for validation* until Jev decides. One job reports a failing check: Jev rejects it, and the retry passes. See the limits below. |
| `loop` | Epochs. A five-node pipeline runs to completion, then `POST /graphs/{id}/reset` starts it over in place as the next epoch, three times. The header shows the epoch. A loop is runs, never an edge. |

**Speed** (0.5×, 1×, 4×) scales the demo's own pauses and work time. It can
change mid-run. Leases and Jev are real time.

**What a single identity can't show.** The page makes every call with one API
key, so every run's worker is the same account. A person or agent can never
vote on a run they worked, and neither can the arbitrator (`403
self_judgment_forbidden`: separation of duties comes before ownership). So:

- The `validation` scenario uses the one judge that is never the worker, Jev
  (`judges.scope: none, jev: true, quorum 1 of 1`). A 2-of-3 quorum of people
  needs three other identities, so it isn't shown.
- If Jev can't decide a run, it escalates to *looking for arbitration*. The
  only arbitrator here is the worker, so the demo stops and says so instead of
  faking a decision.
- The `validation` scenario costs real Jev calls against the tenant's monthly
  cap: about one per node, plus one for the rejected attempt.

## URL parameters

| param | effect |
|---|---|
| `?graph=<id>` | Watch that graph. |
| `?demo=<scenario>` | Demo mode, and start that scenario on load, so a link or a headless check needs no click. Reloading starts a new run. |
| `?play=N` | Kept for back-compatibility: `?demo=plan-100` with an N-node plan. |
| `&speed=0.5\|1\|4` | Demo speed. |

## What you're looking at

- **Layout.** Top to bottom: a prerequisite sits above what it unblocks, so the
  plan fans out downward. ELK's layered algorithm lays it out and minimises
  crossings. When the plan grows, nodes glide to their new place.
- **State.** A node is a dot, and its shading is its **work state**. That's the
  server's `work_state` (one SQL function, `graph_node_work`, migrations
  050/066), which the page draws as it comes. The page never re-derives it from
  status and edges.

  | work state | drawn as |
  |---|---|
  | `looking_for_work` | ringed dot: on the frontier, claimable |
  | ↳ after a failed attempt | red ringed dot: offered again |
  | ↳ `reclaimable: true` | amber dashed ring, turning slowly: a run whose lease lapsed, which the next claim takes over |
  | `claimed` | spinning and glowing: running under a live lease |
  | `looking_for_validation` | violet, turning: reported, and the judges haven't decided |
  | `looking_for_arbitration` | violet with an amber core, breathing: validation escalated, and a person must decide |
  | `not_yet_available` | hollow ring: a prerequisite isn't finished (a dotted blue ring means it's time-gated, with a known `opens_at`) |
  | `finished` | teal: done (a dashed grey ring means cancelled) |

  The header counts the drawn states. From `GET /graphs/{id}/summary` it adds
  the epoch (once past 1) and `critical path`, the longest chain of unfinished
  nodes. A cyclic (`graph`-mode) graph has no work states, so it's drawn by
  status alone.
- **Heat.** When a node is claimed, pulses run down its incoming edges into it,
  and the edges feeding it burn red. When it finishes, pulses flow out to what
  it releases. Idle things cool down. When the plan completes, a wave of heat
  runs from top to bottom.

## Inspecting

- **Hover a node** to see its work state and status, how long it has been in
  that state, the lease countdown while it runs, the latest verdict, and what
  it needs and unblocks.
- **Hover an edge** to see which way the dependency goes and what the edge is
  carrying right now.
- **Click a node** to open a drawer on the right. It shows metadata,
  prerequisites and dependents as chips (a chip selects that node and moves the
  camera there), every run with its epoch, duration, runner, verdict and
  validation state, the facts recorded on the node, and its `data`. The drawer's
  queries sit under `/graphs/{id}/`, so the stream keeps them current. Esc or a
  click on empty space closes it.
- **Pan or zoom** and the view stops following the graph. **⤢ follow** turns
  following back on.

## The sync badge

The pill in the header answers one question: is the page showing the server's
state right now? Click it for details.

| badge | meaning |
|---|---|
| `✓ in sync` | The drawn data was read after the last event, and the last verification passed. |
| `↻ catching up` | An event arrived and its refetch is still in flight (normal during a busy run). |
| `⚠ behind` | An event arrived more than 5 s ago and no read has caught up since, or the stream is in error or revoked. |
| `⚠ drift` | A verification found a difference, and a recheck 3 s later found it again. |

**Verification** runs every 20 s, but only in a quiet moment: at least 1.5 s
after the last event, and only once the page has reread since that event. It
reads the whole graph again (every page, by cursor) and the server's
one-statement summary, both outside the page's cache, and checks two things:

- Every drawn node has the `status` **and** `work_state` the server holds.
- The read is complete and agrees with the summary: the same number of live
  nodes and the same count in every work state.

A check that an event interrupts counts for nothing, and a mismatch has to
survive a recheck. So a change caught in flight is never reported as drift.
Some work states change with **the clock** and no event: a lease lapsing makes
a node reclaimable, and an `opens_at` passing makes it available. The badge
recognises these from the drawn row's own timestamps and lists them as moved by
the clock, not as drift. The page also rereads at that instant (see below).

An amber dot marks **warnings**:

- nodes the server calls **reclaimable** (a worker is presumed gone)
- `running` nodes with **no lease** at all (a silent stall; the summary's
  `leases.missing`)
- runs **escalated** to an arbitrator
- a graph so large it passed the page's read cap (20,000 nodes)

## How state is kept honest

`useGraphStream` is a clock, not a data source. Each activity event invalidates
every query under `/graphs/{id}/`, so the page's whole-graph reads, the
summary, and the drawer's node, runs and facts all refetch through the ordinary
REST reads. A burst of events causes one refetch. On top of that there's a
15 s `refetchInterval` as a safety net, a reread scheduled at the next drawn
lease expiry or `opens_at` (clock changes send no event), and a reconnect that
resumes with `Last-Event-ID`. The picture is always the server's state, never
something assembled from events.

**Every node, not one page.** Nodes are read from `GET /nodes/query`
(`graph_id=<id>&sort=created_at`), which pages with an opaque keyset cursor:
each page's `next` goes back as `after` until there is none (`src/read.ts`).
Keyset paging stays exact under concurrent writes, and `created_at` never
changes, so a node updated during the read can't jump pages. The read uses
their own query keys under `/graphs/{id}/`, which is what lets the stream
invalidate them. Edges have no cursor, so they page by offset against
`meta.total`.

What a real app changes is one line: `configure()` in `src/main.tsx` points at
the gateway and passes `getToken: () => <the user's session>` instead of
relying on this dev proxy. Everything else carries over.

## The API key stays out of the browser

`vite.config.ts` proxies `/api/v1/graph` to `GRAPH_BASE_URL` (default
`https://api.instruxi.dev`) and adds `X-API-Key` there. The page never holds
the key, and the API needs no CORS allowance. `GRAPH_LIVE_LOG=1 npm run dev`
prints one line per proxied request (method and path, never the key). That's
how to confirm Watch mode only ever issues `GET`s and the stream.

While a demo is driving a plan, the page holds the completion banner until the
driver stops. A growing plan can look complete for a moment between bursts,
because a node still being wired is `cancelled`, which the server counts as
finished.
