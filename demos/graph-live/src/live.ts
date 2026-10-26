// The live model of one graph, kept OUTSIDE React. Structure and state
// changes bump `version` (React re-renders); heat and particles are animated
// every frame by anim.ts writing straight to the DOM, so 100 nodes glowing at
// 60 fps never touch React's reconciler.
import type { ApiEdge, ApiNode, WorkState } from "./api";
import type { GraphStreamEvent, GraphStreamStatus } from "@instruxi-io/graph-hooks";

/**
 * How a node is DRAWN, from the server's work_state (never re-derived from
 * status and edges). Each look is one CSS class on the dot.
 */
export type Look =
  | "ready" | "reclaimable" | "retry" // looking_for_work
  | "running" // claimed
  | "verifying" | "arbitration" // looking_for_validation / _arbitration
  | "waiting" | "gated" // not_yet_available (gated: opens_at is known)
  | "done" | "cancelled"; // finished

export function lookOf(n: ApiNode): Look {
  switch (n.work_state) {
    case "looking_for_work":
      return n.reclaimable ? "reclaimable" : n.status === "failed" ? "retry" : "ready";
    case "claimed":
      return "running";
    case "looking_for_validation":
      return "verifying";
    case "looking_for_arbitration":
      return "arbitration";
    case "not_yet_available":
      return n.opens_at ? "gated" : "waiting";
    case "finished":
      return n.status === "cancelled" ? "cancelled" : "done";
  }
  // No work_state: a graph-mode (cyclic) graph, where the frontier and so the
  // work states are undefined. Draw the raw status and claim nothing more.
  if (n.status === "done") return "done";
  if (n.status === "cancelled") return "cancelled";
  if (n.status === "running") return "running";
  if (n.status === "verifying") return "verifying";
  if (n.status === "failed") return "retry";
  return "waiting";
}

export type LiveNode = {
  id: string;
  key: string;
  status: string;
  work: WorkState | null;
  look: Look;
  prereqs: string[];
  children: string[];
  created: number;
  raw: ApiNode; // the full row from the last read: hover and the drawer read it
};

export type Particle = { from: string; to: string; t: number; speed: number; heat: number };
export type Tick = { at: number; text: string; color: string };

export const edgeId = (prereq: string, dependent: string) => `${prereq}->${dependent}`;

export class Live {
  title = "";
  edgeType = "requires";
  nodes = new Map<string, LiveNode>();
  byKey = new Map<string, string>();
  heat = new Map<string, number>();
  phase = new Map<string, number>();
  lastEmit = new Map<string, number>();
  flared = new Set<string>();
  particles: Particle[] = [];
  ticker: Tick[] = [];
  runNode = new Map<string, string>();
  link = "connecting";
  seq = 0;
  events = 0;
  finaleAt: number | null = null;
  playing = false;
  /** When the last activity event arrived, and when the drawn data was last read (ms epoch). */
  lastEventAt = 0;
  readAt = 0;
  version = 0;
  private arrived = 0;
  private listeners = new Set<() => void>();

  /**
   * While this page is DRIVING the plan (▶ play), a server graph.completed can
   * be momentary: every node that exists is done while the planner is still
   * wiring the next burst (a node being wired is `cancelled`, which counts as
   * finished). The page knows better than the event here, so it holds the
   * finale until its own player stops. Watching someone else's graph, the
   * event is all there is, and it is shown as it comes.
   */
  setPlaying(on: boolean) {
    this.playing = on;
    this.bump();
  }

  /** The graph's own row: its slug for the header, and which edge type means "depends on". */
  setMeta(title: string, edgeType: string) {
    if (title === this.title && edgeType === this.edgeType) return;
    this.title = title;
    this.edgeType = edgeType;
    this.bump();
  }

  /** The finale shows once the plan is complete AND nothing here is still driving it. */
  finished() {
    return this.finaleAt !== null && !this.playing;
  }

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };
  getVersion = () => this.version;
  private bump() {
    this.version++;
    this.listeners.forEach((f) => f());
  }

  seenAt = new Map<string, number>();

  /**
   * A node still being wired: created `cancelled`, not yet given its edges and
   * released. Drawn, it would sit in layer 0 for a moment and then fly across
   * the picture. A cancelled node older than this is a real cancellation.
   */
  forming(n: LiveNode) {
    return n.status === "cancelled" && performance.now() - (this.seenAt.get(n.id) ?? 0) < 3000;
  }

  /** The nodes to draw. */
  visible(): LiveNode[] {
    return [...this.nodes.values()].filter((n) => !this.forming(n));
  }

  /** Replace the graph with a fresh read; every state change becomes heat and particles. */
  apply(ns: ApiNode[], es: ApiEdge[], readAt = Date.now()) {
    this.readAt = readAt;
    const next = new Map<string, LiveNode>();
    for (const n of ns) {
      const old = this.nodes.get(n.id);
      next.set(n.id, {
        id: n.id, key: n.key, status: n.status, work: n.work_state ?? null, look: lookOf(n),
        prereqs: [], children: [], created: old?.created ?? ++this.arrived, raw: n,
      });
    }
    for (const e of es) {
      if (e.type !== this.edgeType) continue;
      const dep = next.get(e.from_node_id); // edges point FROM the dependent TO its prerequisite
      const pre = next.get(e.to_node_id);
      if (!dep || !pre) continue;
      dep.prereqs.push(pre.id);
      pre.children.push(dep.id);
    }
    const old = this.nodes;
    this.nodes = next;
    this.byKey = new Map([...next.values()].map((n) => [n.key, n.id]));
    let changed = old.size !== next.size;
    for (const n of next.values()) {
      if (!this.seenAt.has(n.id)) this.seenAt.set(n.id, performance.now());
      if (!this.heat.has(n.id)) {
        this.heat.set(n.id, 1);
        this.phase.set(n.id, Math.random() * Math.PI * 2);
      }
      const o = old.get(n.id);
      if (!o || o.look !== n.look || o.prereqs.length !== n.prereqs.length || o.raw.updated_at !== n.raw.updated_at) changed = true;
      if (o && o.look !== n.look) this.transition(n);
    }
    // A plan that completed and then grew (or was reset) is not complete any more.
    if (this.finaleAt !== null && [...next.values()].some((n) => n.look !== "done" && n.look !== "cancelled")) {
      this.finaleAt = null;
      this.flared.clear();
    }
    if (changed) this.bump();
  }

  private transition(n: LiveNode) {
    this.heat.set(n.id, 1);
    if (n.look === "running") {
      for (const p of n.prereqs) this.particles.push({ from: p, to: n.id, t: 0, speed: 0.9 + Math.random() * 0.5, heat: 1 });
    } else if (n.look === "done") {
      for (const c of n.children) this.particles.push({ from: n.id, to: c, t: 0, speed: 0.7 + Math.random() * 0.4, heat: 0.95 });
    }
  }

  say(text: string, color: string) {
    this.ticker = [...this.ticker, { at: Date.now(), text, color }].slice(-7);
    this.bump();
  }

  /** The hook's connection status, shown in the header. */
  setLink(status: GraphStreamStatus, error?: string) {
    const label = status === "open" ? "live" : status;
    if (label === this.link) return;
    this.link = label;
    if (status === "revoked") this.say("stream closed: access revoked", "#fac850");
    else if (status === "error") this.say(`stream error: ${error ?? "unknown"}`, "#f05040");
    this.bump();
  }

  /** One activity event from useGraphStream (contract §3). */
  onEvent(row: GraphStreamEvent) {
    this.lastEventAt = Date.now();
    this.events++;
    this.seq = Math.max(this.seq, row.seq);
    const m = row.metadata ?? {};
    let id = row.resource_id && this.nodes.has(row.resource_id) ? row.resource_id : "";
    if (m.node_id && this.nodes.has(m.node_id)) id = m.node_id;
    // On run.finished, `node` is where the close LEFT the node (verifying,
    // done, failed, cancelled), not its key; the key comes from run.started.
    const finished = row.verb === "run.finished";
    let name = finished ? m.key || "" : m.node || m.key || "";
    if (name && this.byKey.has(name)) id = this.byKey.get(name)!;
    // A run names its node only on run.started; remember it for the finish.
    if (row.resource_id && row.verb.startsWith("run.")) {
      if (name) this.runNode.set(row.resource_id, name);
      else if (this.runNode.has(row.resource_id)) {
        name = this.runNode.get(row.resource_id)!;
        if (!id && this.byKey.has(name)) id = this.byKey.get(name)!;
      }
    }
    if (id) {
      this.heat.set(id, Math.max(this.heat.get(id) ?? 0, 0.8));
      name = this.nodes.get(id)!.key;
    }
    if (row.verb === "graph.completed") this.finaleAt = performance.now();
    if (row.verb === "graph.reset") {
      this.finaleAt = null;
      this.flared.clear();
    }
    if (row.verb === "run.judged" && m.state === "skipped") return;
    let color = "#96a0be";
    if (row.verb === "run.started") color = "#fcba1e";
    else if (row.verb === "run.finished") color = m.status === "failed" ? "#f05040" : "#5ad2aa";
    else if (row.verb === "graph.completed") color = "#fffabe";
    else if (row.verb === "graph.reset") color = "#7cc4fa";
    else if (row.verb === "run.judged" || row.verb.startsWith("run.vote")) color = m.state === "rejected" ? "#f05040" : "#b89cff";
    else if (row.verb.startsWith("observation.")) color = "#aa8cfa";
    else if (row.verb.startsWith("node.") || row.verb.startsWith("edge.")) color = "#7c86a8";
    const outcome = finished && m.node ? `${m.status} → ${m.node}` : m.status || m.state;
    this.say([row.verb, name, outcome].filter(Boolean).join("  "), color);
  }

  /**
   * The next instant a drawn work_state changes ON THE CLOCK, with no event to
   * say so: a claimed node's lease lapsing (it becomes looking_for_work,
   * reclaimable) or a time-gated node's opens_at passing. The page schedules a
   * reread for then. null when nothing drawn is waiting on the clock.
   */
  nextClockAt(now = Date.now()): number | null {
    let at: number | null = null;
    for (const n of this.nodes.values()) {
      const iso = n.look === "running" ? n.raw.lease_expires_at : n.look === "gated" ? n.raw.opens_at : null;
      if (!iso) continue;
      const t = new Date(iso).getTime();
      if (t > now - 60_000 && (at === null || t < at)) at = t;
    }
    return at;
  }

  depths(): Map<string, number> {
    const d = new Map<string, number>();
    const visiting = new Set<string>();
    const walk = (id: string): number => {
      if (d.has(id)) return d.get(id)!;
      if (visiting.has(id)) return 0;
      visiting.add(id);
      let v = 0;
      for (const p of this.nodes.get(id)?.prereqs ?? []) if (this.nodes.has(p)) v = Math.max(v, walk(p) + 1);
      visiting.delete(id);
      d.set(id, v);
      return v;
    };
    for (const id of this.nodes.keys()) walk(id);
    return d;
  }
}

// An inferno-like ramp: cold violet → red → orange → near-white. Heat is work.
const STOPS: [number, [number, number, number]][] = [
  [0, [24, 14, 58]],
  [0.25, [110, 30, 120]],
  [0.5, [196, 58, 82]],
  [0.7, [240, 112, 36]],
  [0.86, [252, 186, 30]],
  [1, [255, 250, 190]],
];
export function thermal(t: number): [number, number, number] {
  t = Math.min(1, Math.max(0, t));
  for (let i = 1; i < STOPS.length; i++) {
    if (t <= STOPS[i][0]) {
      const [ta, a] = STOPS[i - 1];
      const [tb, b] = STOPS[i];
      const u = (t - ta) / (tb - ta);
      return [0, 1, 2].map((k) => Math.round(a[k] + (b[k] - a[k]) * u)) as [number, number, number];
    }
  }
  return STOPS[STOPS.length - 1][1];
}
export const css = ([r, g, b]: [number, number, number], a = 1) => `rgba(${r},${g},${b},${a})`;
