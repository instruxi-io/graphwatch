import {
  Background, BackgroundVariant, ReactFlow, ReactFlowProvider, useReactFlow,
  type Edge, type Node,
} from "@xyflow/react";
import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useGetGraph, useGetGraphSummary, useGraphStream, type GraphStreamStatus } from "@instruxi-io/graph-hooks";
import { body, type ApiGraph, type ApiSummary } from "./api";
import { startAnimation } from "./anim";
import { archiveDemoGraph, Demo } from "./demo/driver";
import { edgesKey, nodesKey, readAllEdges, readAllNodes } from "./read";
import { SCENARIOS, scenarioById } from "./scenarios";
import { DotNode, type DotData } from "./DotNode";
import { FlowEdge, type FlowData } from "./FlowEdge";
import { GraphPicker } from "./GraphPicker";
import { SyncBadge } from "./SyncBadge";
import { Drawer, edgeState, HoverCard, type Hover } from "./Inspect";
import { layout, NODE_H, NODE_W } from "./layout";
import { Live } from "./live";

const nodeTypes = { dot: DotNode };
const edgeTypes = { flow: FlowEdge };

function Canvas({ live, graphId }: { live: Live; graphId: string }) {
  const version = useSyncExternalStore(live.subscribe, live.getVersion);
  const [pos, setPos] = useState(new Map<string, { x: number; y: number }>());
  const { fitBounds, setCenter, getNode } = useReactFlow();
  const touched = useRef(false);
  const [hover, setHover] = useState<Hover>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [following, setFollowing] = useState(true);
  // Fit to where the layout PUT the nodes, not to where React Flow has
  // measured them: mid-tween and with fresh nodes still unmeasured, fitView
  // fits a corner of the graph at maximum zoom.
  const target = useRef(new Map<string, { x: number; y: number }>());
  const fitTarget = (duration = 700) => {
    const ps = [...target.current.values()];
    if (!ps.length) return;
    const x0 = Math.min(...ps.map((p) => p.x)), y0 = Math.min(...ps.map((p) => p.y));
    const x1 = Math.max(...ps.map((p) => p.x)) + NODE_W, y1 = Math.max(...ps.map((p) => p.y)) + NODE_H;
    fitBounds({ x: x0, y: y0, width: x1 - x0, height: y1 - y0 }, { padding: 0.12, duration });
  };
  const follow = () => {
    touched.current = false;
    setFollowing(true);
    fitTarget(600);
  };

  // Selecting from the drawer (a prerequisite or dependent chip) also moves
  // the camera there.
  const select = (id: string) => {
    setSelected(id);
    const n = getNode(id);
    if (n) {
      touched.current = true;
      setFollowing(false);
      setCenter(n.position.x + NODE_W / 2, n.position.y + 20, { zoom: 1.4, duration: 600 });
    }
  };

  // Layout only when the SET of nodes or edges changes.
  const shape = useMemo(
    () => live.visible().map((n) => `${n.id}:${n.prereqs.join(",")}`).sort().join("|"),
    [version, live],
  );
  // Positions are TWEENED in state rather than glided with a CSS transition:
  // React Flow draws edges from the positions it holds, so a CSS glide leaves
  // every edge pointing at where its node is going while the dot is still on
  // its way there.
  const shown = useRef(new Map<string, { x: number; y: number }>());
  const tween = useRef(0);
  const glide = (target: Map<string, { x: number; y: number }>) => {
    cancelAnimationFrame(tween.current);
    const from = new Map<string, { x: number; y: number }>();
    for (const [id, p] of target) {
      let f = shown.current.get(id);
      if (!f) for (const pre of live.nodes.get(id)?.prereqs ?? []) if ((f = shown.current.get(pre))) break;
      from.set(id, f ?? p); // born at its prerequisite
    }
    const t0 = performance.now();
    const step = (now: number) => {
      const u = Math.min(1, (now - t0) / 650);
      const e = 1 - Math.pow(1 - u, 3);
      const cur = new Map<string, { x: number; y: number }>();
      for (const [id, p] of target) {
        const f = from.get(id)!;
        cur.set(id, { x: f.x + (p.x - f.x) * e, y: f.y + (p.y - f.y) * e });
      }
      shown.current = cur;
      setPos(cur);
      if (u < 1) tween.current = requestAnimationFrame(step);
    };
    tween.current = requestAnimationFrame(step);
  };
  useEffect(() => () => cancelAnimationFrame(tween.current), []);

  useEffect(() => {
    if (!live.nodes.size) return;
    let cancelled = false;
    const t = setTimeout(async () => {
      const next = await layout(live.visible());
      if (cancelled) return;
      target.current = next;
      glide(next);
      if (!touched.current) fitTarget();
    }, 120);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shape, live]);

  const nodes: Node<DotData>[] = useMemo(() => {
    return live.visible().map((n) => {
      // Not laid out yet: born at its prerequisite, then it glides into place.
      let p = pos.get(n.id);
      if (!p) for (const pre of n.prereqs) if ((p = pos.get(pre))) break;
      return {
        id: n.id, type: "dot", position: p ?? { x: 0, y: 0 }, draggable: false, selectable: false,
        data: { key: n.key, look: n.look, selected: n.id === selected },
      };
    });
  }, [version, pos, live, selected]);

  const edges: Edge<FlowData>[] = useMemo(() => {
    const out: Edge<FlowData>[] = [];
    for (const n of live.visible()) {
      for (const p of n.prereqs) {
        const from = live.nodes.get(p);
        if (!from || live.forming(from)) continue;
        const state = edgeState(live, from, n);
        const linked = selected !== null && (p === selected || n.id === selected);
        out.push({ id: `${p}->${n.id}`, source: p, target: n.id, type: "flow", data: { state, linked } });
      }
    }
    return out;
  }, [version, live, selected]);

  useEffect(() => startAnimation(live), [live, graphId]);

  const at = (e: React.MouseEvent) => ({ x: e.clientX, y: e.clientY });
  return (
    <>
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      nodesConnectable={false}
      nodesFocusable={false}
      autoPanOnNodeFocus={false}
      onNodeMouseEnter={(e, n) => setHover({ kind: "node", id: n.id, ...at(e) })}
      onNodeMouseMove={(e, n) => setHover({ kind: "node", id: n.id, ...at(e) })}
      onNodeMouseLeave={() => setHover(null)}
      onEdgeMouseEnter={(e, ed) => setHover({ kind: "edge", from: ed.source, to: ed.target, ...at(e) })}
      onEdgeMouseMove={(e, ed) => setHover({ kind: "edge", from: ed.source, to: ed.target, ...at(e) })}
      onEdgeMouseLeave={() => setHover(null)}
      onNodeClick={(_, n) => setSelected(n.id)}
      onPaneClick={() => setSelected(null)}
      onMove={(e) => { if (e) { touched.current = true; setFollowing(false); } }}
      minZoom={0.15}
      maxZoom={2.5}
      fitView
    >
      <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#1d1f33" />
    </ReactFlow>
    {!following && <button className="follow" onClick={follow} title="fit the whole graph and follow it as it grows">⤢ follow</button>}
    <HoverCard live={live} hover={hover} />
    {selected && live.nodes.has(selected) && (
      <Drawer key={selected} live={live} graphId={graphId} id={selected} onSelect={select} onClose={() => setSelected(null)} />
    )}
    </>
  );
}

type Mode = "watch" | "demo";
const SPEEDS = [0.5, 1, 4];

function Counts({ live, summary }: { live: Live; summary: ApiSummary | undefined }) {
  useSyncExternalStore(live.subscribe, live.getVersion);
  // Counted from what is DRAWN (the server's work_state on each row), so the
  // numbers always match the picture; the summary adds what rows cannot say.
  const c = { done: 0, running: 0, validation: 0, work: 0, reclaimable: 0, waiting: 0 };
  for (const n of live.nodes.values()) {
    if (live.forming(n)) continue;
    if (n.look === "done" || n.look === "cancelled") c.done++;
    else if (n.look === "running") c.running++;
    else if (n.look === "verifying" || n.look === "arbitration") c.validation++;
    else if (n.look === "ready" || n.look === "retry" || n.look === "reclaimable") {
      c.work++;
      if (n.look === "reclaimable") c.reclaimable++;
    } else c.waiting++;
  }
  const epoch = summary?.epoch ?? 1;
  const cp = summary?.critical_path?.remaining;
  return (
    <div className="counts">
      <span className="c done" title="finished">● {c.done} finished</span>
      <span className="c running" title="claimed: running under a live lease">◐ {c.running} claimed</span>
      {c.validation > 0 && <span className="c verifying" title="looking for validation (or arbitration)">◈ {c.validation} validating</span>}
      <span className="c ready" title="looking for work: on the frontier">◎ {c.work} looking for work{c.reclaimable ? ` (${c.reclaimable} reclaimable)` : ""}</span>
      <span className="c waiting" title="not yet available">○ {c.waiting} not yet</span>
      {epoch > 1 && <span className="c epoch" title="GET /graphs/{id}/summary: epoch">epoch {epoch}</span>}
      {cp !== undefined && cp > 0 && <span className="c waiting" title="summary.critical_path.remaining: the longest chain of unfinished nodes">critical path {cp}</span>}
    </div>
  );
}

function Header(p: {
  live: Live; mode: Mode; setMode: (m: Mode) => void; graphId: string; stream: GraphStreamStatus; isDag: boolean;
  summary: ApiSummary | undefined;
  // watch
  onPick: (id: string) => void; hideDemos: boolean; setHideDemos: (b: boolean) => void;
  // demo
  scenario: string; setScenario: (id: string) => void; speed: number; setSpeed: (n: number) => void;
  size: number; setSize: (n: number) => void; running: boolean; onRun: () => void; onStop: () => void;
  onArchive: () => void; onWatch: () => void;
}) {
  const { live, mode, graphId } = p;
  useSyncExternalStore(live.subscribe, live.getVersion);
  const sc = scenarioById(p.scenario);
  return (
    <header>
      <div className="brand"><span className="mark">◉</span> graph live</div>
      <div className="modes" role="tablist">
        <button role="tab" className={mode === "watch" ? "on" : ""} onClick={() => p.setMode("watch")} title="watch an existing graph; never writes">watch</button>
        <button role="tab" className={mode === "demo" ? "on" : ""} onClick={() => p.setMode("demo")} title="create a demo graph and drive it">demo</button>
      </div>
      {mode === "watch" ? (
        <>
          <GraphPicker current={graphId} title={live.title} onPick={p.onPick} hideDemos={p.hideDemos} setHideDemos={p.setHideDemos} />
          {graphId && <span className="mode" title="this page only reads and listens">read-only</span>}
        </>
      ) : (
        <div className="demo">
          <select value={p.scenario} onChange={(e) => p.setScenario(e.target.value)} disabled={p.running} title={sc?.description}>
            {SCENARIOS.map((s) => <option key={s.id} value={s.id} title={s.description}>{s.id}</option>)}
          </select>
          {p.scenario === "plan-100" && (
            <input type="number" min={5} max={300} value={p.size} onChange={(e) => p.setSize(+e.target.value)} disabled={p.running} title="plan size" />
          )}
          <select value={p.speed} onChange={(e) => p.setSpeed(+e.target.value)} title="demo speed (leases stay real time)">
            {SPEEDS.map((x) => <option key={x} value={x}>{x}×</option>)}
          </select>
          {p.running
            ? <button onClick={p.onStop}>■ stop</button>
            : <button onClick={p.onRun}>▶ run</button>}
          {!p.running && graphId && (
            <>
              <button className="quiet" onClick={p.onWatch} title="watch this demo graph in Watch mode">watch it</button>
              <button className="quiet" onClick={p.onArchive} title="DELETE /graphs/{id}: archive this demo graph">archive</button>
            </>
          )}
        </div>
      )}
      <Counts live={live} summary={p.summary} />
      <div className="right">
        {graphId && <SyncBadge live={live} graphId={graphId} isDag={p.isDag} stream={p.stream} />}
        <span className={`link ${live.link === "live" ? "ok" : "warn"}`}>{live.link}</span>
        <span className="seq">seq {live.seq} · {live.events} events</span>
      </div>
    </header>
  );
}

function Ticker({ live }: { live: Live }) {
  useSyncExternalStore(live.subscribe, live.getVersion);
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((x) => x + 1), 500);
    return () => clearInterval(t);
  }, []);
  const now = Date.now();
  return (
    <div className="ticker">
      {live.ticker.map((t, i) => (
        <div key={t.at + ":" + i} style={{ color: t.color, opacity: Math.max(0.25, 1 - (now - t.at) / 12000) }}>
          <span className="time">{new Date(t.at).toLocaleTimeString([], { hour12: false })}</span> {t.text}
        </div>
      ))}
    </div>
  );
}

function readParams() {
  const q = new URLSearchParams(location.search);
  const play = Number(q.get("play"));
  const demo = q.get("demo") ?? (play > 0 ? "plan-100" : "");
  const speed = Number(q.get("speed"));
  return {
    mode: (demo ? "demo" : "watch") as Mode,
    graph: q.get("graph") ?? "",
    scenario: scenarioById(demo)?.id ?? SCENARIOS[0].id,
    autostart: !!scenarioById(demo),
    size: play > 0 ? play : 100,
    speed: SPEEDS.includes(speed) ? speed : 1,
  };
}

export function App() {
  const init = useMemo(readParams, []);
  const [mode, setModeState] = useState<Mode>(init.mode);
  const [watchId, setWatchId] = useState(init.graph);
  const [demoId, setDemoId] = useState("");
  const [scenario, setScenario] = useState(init.scenario);
  const [speed, setSpeed] = useState(init.speed);
  const [size, setSize] = useState(init.size);
  const [running, setRunning] = useState(false);
  const [hideDemos, setHideDemos] = useState(false);
  const graphId = mode === "watch" ? watchId : demoId;
  const live = useMemo(() => new Live(), [graphId]);
  const liveRef = useRef(live);
  liveRef.current = live;
  const speedRef = useRef(speed);
  speedRef.current = speed;
  const run = useRef<AbortController | null>(null);
  const qc = useQueryClient();
  // Dev only: lets a test corrupt the drawn state to prove the drift badge fires.
  if (import.meta.env.DEV) (window as unknown as { __live: Live }).__live = live;
  const version = useSyncExternalStore(live.subscribe, live.getVersion);

  // The URL says what the page is doing: ?graph=<id> watches, ?demo=<scenario> runs one.
  useEffect(() => {
    const q = mode === "watch" ? (watchId ? `?graph=${watchId}` : "") : `?demo=${scenario}${speed !== 1 ? `&speed=${speed}` : ""}`;
    history.replaceState(null, "", q || location.pathname);
  }, [mode, watchId, scenario, speed]);

  // The published hooks and generated functions do the reading. useGraphStream
  // is the clock: each activity event invalidates every query under
  // /graphs/{id}/ — the whole-graph reads below included — and they refetch.
  const on = !!graphId;
  const graph = useGetGraph(graphId, { query: { enabled: on } });
  const isDag = (body<{ data: ApiGraph }>(graph.data)?.data?.mode ?? "dag") === "dag";
  // A safety refetch every 15 s, in case the stream is ever quiet when it
  // should not be; normally the stream triggers the refetch within ~100 ms.
  const nodes = useQuery({ queryKey: nodesKey(graphId), queryFn: ({ signal }) => readAllNodes(graphId, signal), enabled: on, refetchInterval: 15000 });
  const edges = useQuery({ queryKey: edgesKey(graphId), queryFn: ({ signal }) => readAllEdges(graphId, signal), enabled: on, refetchInterval: 15000 });
  // The server's one-statement snapshot: epoch, critical path, escalations. A
  // graph-mode graph has none (409 graph_not_acyclic), so it is not asked.
  const summaryQ = useGetGraphSummary(graphId, undefined, { query: { enabled: on && isDag, refetchInterval: 15000, retry: false } });
  const summary = body<{ data: ApiSummary }>(summaryQ.data)?.data;
  const stream = useGraphStream(graphId || undefined, { onEvent: (e) => live.onEvent(e) });

  useEffect(() => {
    const g = body<{ data: ApiGraph }>(graph.data)?.data;
    if (g) live.setMeta(g.slug, g.dependency_edge_type || "requires");
    // Applied AFTER the edge type is known: apply() keeps only dependency edges.
    if (nodes.data && edges.data) live.apply(nodes.data.nodes, edges.data.edges, Math.min(nodes.dataUpdatedAt, edges.dataUpdatedAt));
    // Keyed on dataUpdatedAt, not data: a reread that returns the same rows
    // keeps the same object (structural sharing), and it must STILL be applied
    // — it is what corrects a drawn state that drifted, and what "last read"
    // on the sync badge reports.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes.dataUpdatedAt, edges.dataUpdatedAt, graph.dataUpdatedAt, live]);
  useEffect(() => live.setLink(stream.status, stream.error?.message), [stream.status, stream.error, live]);

  // The CLOCK moves some work states with no event: a lease lapsing turns a
  // claimed node reclaimable, an opens_at passing makes a gated one available.
  // Reread at that instant instead of waiting for the 15 s safety net.
  useEffect(() => {
    if (!on) return;
    const at = live.nextClockAt();
    if (at === null) return;
    const t = setTimeout(() => void qc.invalidateQueries({ queryKey: nodesKey(graphId) }), Math.max(1000, at - Date.now() + 750));
    return () => clearTimeout(t);
  }, [version, live, on, graphId, qc]);

  // ---- demo mode: the only code path that writes ----
  const stop = () => {
    run.current?.abort();
    run.current = null;
  };
  useEffect(() => stop, []);
  const onRun = async () => {
    stop();
    const ac = new AbortController();
    run.current = ac;
    const s = scenarioById(scenario)!;
    setRunning(true);
    let driven: Live | null = null;
    try {
      const id = await s.build({ signal: ac.signal, size });
      if (ac.signal.aborted) return;
      setDemoId(id);
      await new Promise((r) => setTimeout(r, 400)); // let the new Live subscribe first
      driven = liveRef.current;
      driven.setPlaying(true);
      await s.drive(new Demo(id, () => liveRef.current, ac.signal, () => speedRef.current), { signal: ac.signal, size });
    } catch (e) {
      if (!ac.signal.aborted) liveRef.current.say(`demo: ${(e as Error).message}`, "#f05040");
    } finally {
      driven?.setPlaying(false);
      if (run.current === ac) run.current = null;
      setRunning(false);
    }
  };
  const setMode = (m: Mode) => {
    if (m === mode) return;
    stop(); // leaving Demo never leaves a player running in the background
    setModeState(m);
  };
  const onArchive = async () => {
    const id = demoId;
    try {
      await archiveDemoGraph(id);
      setDemoId("");
    } catch (e) {
      live.say(`archive: ${(e as Error).message}`, "#f05040");
    }
  };

  // ?demo=<scenario> (or the older ?play=N) starts a run on load, so a link
  // (or a headless check) needs no click. Deferred a tick and cancelled in the
  // cleanup, so StrictMode's mount → unmount → mount starts exactly one run.
  useEffect(() => {
    if (!init.autostart) return;
    const t = setTimeout(() => void onRun(), 0);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="app">
      <Header
        live={live} mode={mode} setMode={setMode} graphId={graphId} stream={stream.status} isDag={isDag} summary={summary}
        onPick={setWatchId} hideDemos={hideDemos} setHideDemos={setHideDemos}
        scenario={scenario} setScenario={setScenario} speed={speed} setSpeed={setSpeed} size={size} setSize={setSize}
        running={running} onRun={onRun} onStop={stop} onArchive={onArchive}
        onWatch={() => { setWatchId(demoId); setModeState("watch"); }}
      />
      <main>
        {graphId ? (
          <ReactFlowProvider>
            <Canvas live={live} graphId={graphId} />
          </ReactFlowProvider>
        ) : (
          <div className="empty">
            {mode === "watch"
              ? "pick a graph above to watch it; this mode only reads"
              : `${scenarioById(scenario)?.description ?? ""} — press ▶ run`}
          </div>
        )}
        {live.finished() && <div className="finale">✦ plan complete ✦</div>}
        <Ticker live={live} />
      </main>
      <span hidden>{version}</span>
    </div>
  );
}
