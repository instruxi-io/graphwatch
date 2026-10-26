// The demo DRIVER: the primitives every scenario is built from — a graph, a
// batch of nodes wired without ever exposing one half-wired, and workers that
// claim from the frontier, hold a lease, and report. All of it through the
// published hooks' generated functions, exactly what a set of harnesses would
// call. Only Demo mode calls into this file: watching never writes.
import {
  archiveGraph, batchCreateEdges, batchCreateNodes, claimGraphFrontier, completeRun, createGraph,
  getGraphSummary, heartbeatGraphRun, setNodeStatus,
} from "@instruxi-io/graph-hooks";
import { body, retry, timeout, type ApiNode, type ApiSummary } from "../api";
import type { Live } from "../live";

export type NodeSpec = {
  key: string;
  prereqs?: string[];
  title?: string;
  type?: string;
  description?: string;
  data?: Record<string, unknown>;
  lease_seconds?: number;
};

export type Claimed = {
  node_id: string;
  run_id: string;
  key: string;
  title?: string;
  attempt?: number;
  reclaimed_run_id?: string;
  data?: Record<string, unknown>;
};

export type Evidence = { kind: "command" | "note"; cmd?: string; exit?: number; output?: string; text?: string };
export type Outcome =
  | { status: "succeeded"; evidence?: Evidence[] }
  | { status: "failed"; error: string }
  /** Walk away mid-run: no report, no heartbeat. The lease lapses and the next claim reclaims it. */
  | "crash";

export async function createDemoGraph(scenario: string, title: string, extra: { default_lease_seconds?: number } = {}): Promise<string> {
  const slug = `demo-${scenario}-${Math.floor(Date.now() / 1000)}-${Math.random().toString(16).slice(2, 6)}`;
  const g = body<{ data: { id: string } }>(
    await createGraph({ slug, name: `demo-${scenario} · ${title}`, mode: "dag", ...extra }, timeout()),
  )!;
  return g.data.id;
}

/** DELETE /graphs/{id}: archives, never destroys. Offered by a button, never automatic. */
export const archiveDemoGraph = (graphId: string) => retry(() => archiveGraph(graphId, timeout()));

const pause = (ms: number, signal: AbortSignal) =>
  new Promise<void>((r) => {
    if (signal.aborted) return r();
    const t = setTimeout(r, ms);
    signal.addEventListener("abort", () => (clearTimeout(t), r()), { once: true });
  });

/** One demo run against one graph. */
export class Demo {
  /** Set once the server's summary says the plan is complete (the one rule, graph_is_complete). */
  complete = false;
  /** Set when the plan can no longer move without someone this page cannot be (an arbitrator). */
  stalled = "";

  constructor(
    public graphId: string,
    private liveOf: () => Live,
    public signal: AbortSignal,
    private speedOf: () => number,
  ) {}

  get live() {
    return this.liveOf();
  }
  get stopped() {
    return this.signal.aborted;
  }

  /** Sleep `ms` of DEMO time: 4× speed sleeps a quarter as long. Resolves early on stop. */
  sleep(ms: number) {
    return pause(ms / this.speedOf(), this.signal);
  }

  say(text: string, color = "#96a0be") {
    this.live.say(text, color);
  }

  /**
   * Add nodes without ever exposing one half-wired: `blocked` IS claimable, so
   * they are created `cancelled` (not claimable), wired in one batch, then
   * released as `pending`. Every step is an upsert or a status set, so each
   * retries on its own.
   */
  async addNodes(items: NodeSpec[]) {
    const created = body<{ data: ApiNode[] }>(await retry(() => batchCreateNodes(this.graphId, {
      nodes: items.map((i) => ({
        key: i.key, type: i.type ?? "task", title: i.title ?? i.key, description: i.description,
        data: i.data, lease_seconds: i.lease_seconds, status: "cancelled",
      })),
    }, timeout())))!;
    const edges = items.flatMap((i) => (i.prereqs ?? []).map((p) => ({ from_key: i.key, to_key: p, type: "requires" })));
    if (edges.length) await retry(() => batchCreateEdges(this.graphId, { edges }, timeout()));
    await Promise.all(created.data.map((n) => retry(() => setNodeStatus(this.graphId, n.id, { status: "pending" }, timeout()))));
  }

  /** One claim from the frontier, or null when nothing is runnable. Never retried (see api.ts retry). */
  async claim(runner: string): Promise<Claimed | null> {
    const r = body<{ data: Claimed[] }>(await claimGraphFrontier(this.graphId, { limit: 1, runner }, undefined, timeout()))!;
    return r.data[0] ?? null;
  }

  /** Report an outcome. Reporting the same outcome twice answers 200, so this is safe to retry. */
  report(c: Claimed, o: Exclude<Outcome, "crash">) {
    return retry(() => completeRun(this.graphId, c.node_id, c.run_id, o, undefined, timeout()));
  }

  /**
   * Work for `ms` of demo time while keeping the lease alive: a heartbeat
   * every 10 s of REAL time (the lease is the server's clock, not the demo's).
   */
  async hold(c: Claimed, ms: number) {
    const end = performance.now() + ms / this.speedOf();
    while (!this.stopped && performance.now() < end) {
      await pause(Math.min(10_000, end - performance.now()), this.signal);
      if (!this.stopped && performance.now() < end) await heartbeatGraphRun(this.graphId, c.node_id, c.run_id, timeout()).catch(() => {});
    }
  }

  /**
   * Ask the server whether the plan is finished, or stuck on an arbitrator.
   * Called when a claim comes back empty, so a quiet plan costs one summary
   * read per few empty claims.
   */
  async checkDone() {
    try {
      const s = body<{ data: ApiSummary }>(await getGraphSummary(this.graphId, undefined, timeout()))!.data;
      this.complete = !!s.complete;
      const w = s.by_work_state ?? {};
      const moving = (w.looking_for_work ?? 0) + (w.claimed ?? 0) + (w.looking_for_validation ?? 0);
      this.stalled = !moving && (w.looking_for_arbitration ?? 0) > 0
        ? `${w.looking_for_arbitration} run(s) escalated to an arbitrator; this page's one identity ran them, so it cannot arbitrate (self_judgment_forbidden)`
        : "";
    } catch {
      /* a failed read decides nothing */
    }
  }

  /**
   * A worker: claim, work, report, until the plan is done (or `more()` says
   * the planner is still adding to it), the demo stops, or the plan stalls.
   * An empty claim waits about `idleMs` of real time before the next.
   * A run still held when the demo stops is reported `failed` ("demo
   * stopped"), which leaves its node retryable rather than finished.
   */
  async worker(runner: string, work: (c: Claimed) => Promise<Outcome>, more: () => boolean = () => false, idleMs = 650) {
    let empty = 0;
    while (!this.stopped && !this.stalled && (more() || !this.complete)) {
      let c: Claimed | null;
      try {
        c = await this.claim(runner);
      } catch {
        await this.sleep(1000);
        continue;
      }
      if (!c) {
        if (++empty % 3 === 0 && !more()) await this.checkDone();
        // Real time, not demo time: at 4× a scaled wait would poll the claim
        // endpoint four times as hard for no gain while a lease runs down.
        await pause(idleMs * (0.6 + Math.random() * 0.8), this.signal);
        continue;
      }
      empty = 0;
      if (c.reclaimed_run_id) this.say(`${runner} reclaimed ${c.key} (its lease had lapsed)`, "#fcba1e");
      let out: Outcome;
      try {
        out = await work(c);
      } catch (e) {
        out = { status: "failed", error: `worker error: ${(e as Error).message}` };
      }
      if (out === "crash") {
        this.say(`${runner} crashed holding ${c.key}; its lease will lapse`, "#f05040");
        await this.sleep(1500); // the worker "restarts"
        continue;
      }
      if (this.stopped) out = { status: "failed", error: "demo stopped" };
      await this.report(c, out).catch(() => {});
    }
  }
}
