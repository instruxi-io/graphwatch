// Is what the page draws the server's truth right now? Three checks.
//
// 1. FRESHNESS — has the drawn data been reread since the last event arrived?
//    A short gap is normal (the refetch is in flight); a long one means the
//    page is behind.
// 2. VERIFICATION — every 20 s, in a quiet moment, read the whole graph again
//    (every page, by cursor) and the server's one-statement summary, both
//    independently of the page's cache, then check that
//      a. every drawn node has the status AND work_state the server holds, and
//      b. that read is complete and agrees with the summary: as many live
//         nodes, and the same count in every work state.
//    The page never re-derives a work state or the frontier itself: that rule
//    is the server's (graph_node_work, migrations 050/066, plus the readiness
//    counter), and a copy here would be a second rule to drift.
//    Reads are separate requests, so a change landing between them would look
//    like drift. A check an event interrupted is inconclusive, and a mismatch
//    must survive a recheck before it is reported.
// 3. WARNINGS — a node the server calls reclaimable (a running node past its
//    lease: its harness is presumed gone), a running node with no lease at all
//    (a silent stall), a run escalated to an arbitrator, and a read so large
//    it stopped paging.
import { getGraphSummary } from "@instruxi-io/graph-hooks";
import { body, WORK_STATES, type ApiNode, type ApiSummary, type WorkState } from "./api";
import type { Live, LiveNode } from "./live";
import { readAllNodes } from "./read";

export type SyncState = "verifying" | "in-sync" | "catching-up" | "behind" | "drift";

export type Mismatch = { key: string; drawn: string; server: string };

export interface SyncReport {
  state: SyncState;
  checkedAt: number; // last conclusive verification (ms epoch), 0 = never
  checkedNodes: number;
  pages: number;
  /** Did the paged read and the summary agree? null on a graph-mode graph (no summary). */
  summary: { agree: boolean; live: number; read: number; diffs: string[] } | null;
  mismatches: Mismatch[];
  /** Drawn states the clock has since moved (a lease lapsed, a gate opened); not drift. */
  clock: string[];
  reclaimable: string[];
  missingLeases: number;
  escalated: number;
  partial: { read: number; total: number } | null;
  note: string;
}

export const emptyReport = (): SyncReport => ({
  state: "verifying", checkedAt: 0, checkedNodes: 0, pages: 0, summary: null, mismatches: [], clock: [],
  reclaimable: [], missingLeases: 0, escalated: 0, partial: null, note: "",
});

const past = (iso?: string | null, now = Date.now()) => !!iso && new Date(iso).getTime() <= now;

/**
 * A drawn state the CLOCK has moved since it was read, with no event: the
 * drawn row's own lease lapsed (claimed → looking_for_work, reclaimable), or
 * its own opens_at passed (not_yet_available → looking_for_work). Recognised
 * from the drawn row's timestamps, not by re-deriving the rule; the page
 * rereads at that instant anyway (App's clock refetch).
 */
function clockMoved(d: LiveNode, s: ApiNode, now: number) {
  if (d.work === "claimed" && s.work_state === "looking_for_work" && s.reclaimable) return past(d.raw.lease_expires_at, now);
  if (d.work === "not_yet_available" && s.work_state === "looking_for_work") return past(d.raw.opens_at, now);
  return false;
}

type Verdict = { ok: boolean; report: Partial<SyncReport> } | { inconclusive: string };

/** One verification pass. Never throws; a failed read is inconclusive. */
export async function verify(live: Live, graphId: string, isDag: boolean): Promise<Verdict> {
  const seqBefore = live.seq;
  let read: Awaited<ReturnType<typeof readAllNodes>>;
  let summary: ApiSummary | null = null;
  try {
    const [nr, sr] = await Promise.all([
      readAllNodes(graphId),
      // A graph-mode graph answers 409 graph_not_acyclic: no work states there.
      isDag ? getGraphSummary(graphId) : Promise.resolve(null),
    ]);
    read = nr;
    if (sr) summary = body<{ data: ApiSummary }>(sr)!.data;
  } catch (e) {
    return { inconclusive: `read failed: ${(e as Error).message}` };
  }
  if (live.seq !== seqBefore) return { inconclusive: "the graph changed during the check" };

  const now = Date.now();
  const mismatches: Mismatch[] = [];
  const clock: string[] = [];
  const server = new Map(read.nodes.map((n) => [n.id, n]));
  const said = (n: { status: string; work_state?: string | null }) => `${n.status}/${n.work_state ?? "—"}`;
  for (const [id, d] of live.nodes) {
    const s = server.get(id);
    if (!s) {
      if (read.complete) mismatches.push({ key: d.key, drawn: `${d.status}/${d.work ?? "—"}`, server: "absent" });
      continue;
    }
    if (s.status === d.status && (s.work_state ?? null) === d.work) continue;
    if (clockMoved(d, s, now)) clock.push(d.key);
    else mismatches.push({ key: d.key, drawn: `${d.status}/${d.work ?? "—"}`, server: said(s) });
  }
  for (const [id, s] of server) {
    // A node still being wired is `cancelled` and deliberately not drawn yet.
    if (!live.nodes.has(id) && s.status !== "cancelled") mismatches.push({ key: s.key, drawn: "absent", server: said(s) });
  }

  // The read against the summary: the same snapshot rule, computed server-side
  // in ONE statement. A disagreement here means the paged read missed or
  // doubled rows, or a write landed between the two requests (hence the recheck).
  let sum: SyncReport["summary"] = null;
  if (summary) {
    const counts = new Map<WorkState, number>();
    for (const n of read.nodes) if (n.work_state) counts.set(n.work_state, (counts.get(n.work_state) ?? 0) + 1);
    const diffs: string[] = [];
    for (const w of WORK_STATES) {
      const a = summary.by_work_state?.[w] ?? 0, b = counts.get(w) ?? 0;
      if (a !== b) diffs.push(`${w.replace(/_/g, " ")}: summary ${a}, read ${b}`);
    }
    const liveCount = summary.nodes?.live ?? 0;
    if (liveCount !== read.nodes.length) diffs.unshift(`live nodes: summary ${liveCount}, read ${read.nodes.length}`);
    sum = { agree: !diffs.length, live: liveCount, read: read.nodes.length, diffs };
  }

  return {
    ok: !mismatches.length && (sum?.agree ?? true),
    report: {
      checkedAt: now, checkedNodes: read.nodes.length, pages: read.pages, summary: sum, mismatches, clock,
      reclaimable: read.nodes.filter((n) => n.reclaimable).map((n) => n.key),
      missingLeases: summary?.leases?.missing ?? 0,
      escalated: summary?.validation?.escalated ?? 0,
      partial: read.complete ? null : { read: read.nodes.length, total: read.total },
    },
  };
}
