import { useEffect, useState, useSyncExternalStore } from "react";
import { useGetNode, useListNodeObservations, useListNodeRuns } from "@instruxi-io/graph-hooks";
import { body, type ApiNode, type ApiObservation, type ApiRun } from "./api";
import type { Live, LiveNode } from "./live";

export type Hover =
  | { kind: "node"; id: string; x: number; y: number }
  | { kind: "edge"; from: string; to: string; x: number; y: number }
  | null;

/** The CSS look a node is drawn in (the server's work_state, live.ts lookOf). */
export function shown(_live: Live, n: LiveNode): string {
  return n.look;
}

/** The server's work state in words, with the detail beside it. */
export function stateText(n: LiveNode): string {
  const w = n.work;
  if (!w) return `status ${n.status}`; // graph-mode graph: no work states
  const words = w.replace(/_/g, " ");
  if (w === "looking_for_work" && n.raw.reclaimable) return `${words} · reclaimable`;
  if (w === "looking_for_work" && n.status === "failed") return `${words} · retry`;
  if (w === "not_yet_available" && n.raw.opens_at) return `${words} · opens in ${until(n.raw.opens_at)}`;
  if (w === "finished" && n.status === "cancelled") return `${words} · cancelled`;
  return words;
}

function ago(iso?: string | null): string {
  if (!iso) return "—";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return `${Math.round(s)}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  return `${Math.round(s / 3600)}h`;
}

function until(iso?: string | null): string {
  if (!iso) return "—";
  const s = (new Date(iso).getTime() - Date.now()) / 1000;
  return s <= 0 ? "lapsed" : s < 60 ? `${Math.round(s)}s` : `${Math.round(s / 60)}m`;
}

function dur(a: string, b?: string | null) {
  const s = ((b ? new Date(b).getTime() : Date.now()) - new Date(a).getTime()) / 1000;
  return s < 60 ? `${s.toFixed(1)}s` : `${Math.floor(s / 60)}m${Math.round(s % 60)}s`;
}

const EDGE_STATE: Record<string, string> = {
  feeding: "feeding — the dependent is running on it",
  settled: "settled — both sides finished",
  released: "released — prerequisite finished, dependent's turn",
  idle: "waiting — the prerequisite is not finished yet",
};

const finished = (n: LiveNode) => n.look === "done" || n.look === "cancelled";

export function edgeState(_live: Live, from: LiveNode, to: LiveNode): "feeding" | "settled" | "released" | "idle" {
  if (to.look === "running") return "feeding";
  if (finished(from) && finished(to)) return "settled";
  if (finished(from)) return "released";
  return "idle";
}

// A tick every second so "in state for" and lease countdowns move.
function useSecond() {
  const [, set] = useState(0);
  useEffect(() => {
    const t = setInterval(() => set((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, []);
}

export function HoverCard({ live, hover }: { live: Live; hover: Hover }) {
  useSyncExternalStore(live.subscribe, live.getVersion);
  useSecond();
  if (!hover) return null;
  const style = { left: Math.min(hover.x + 16, innerWidth - 300), top: Math.min(hover.y + 14, innerHeight - 180) };

  if (hover.kind === "edge") {
    const a = live.nodes.get(hover.from);
    const b = live.nodes.get(hover.to);
    if (!a || !b) return null;
    return (
      <div className="hover" style={style}>
        <div className="h-title">
          <span className={`pill ${shown(live, a)}`}>{a.key}</span> → <span className={`pill ${shown(live, b)}`}>{b.key}</span>
        </div>
        <div className="h-row"><span>type</span>{live.edgeType}</div>
        <div className="h-row"><span>means</span>{b.key} needs {a.key}</div>
        <div className="h-note">{EDGE_STATE[edgeState(live, a, b)]}</div>
      </div>
    );
  }

  const n = live.nodes.get(hover.id);
  if (!n) return null;
  const r = n.raw;
  const heat = live.heat.get(n.id) ?? 0;
  return (
    <div className="hover" style={style}>
      <div className="h-title">
        {n.key} <span className={`pill ${shown(live, n)}`}>{n.work ? n.work.replace(/_/g, " ") : n.status}</span>
      </div>
      {r.title && r.title !== n.key && <div className="h-sub">{r.title}</div>}
      <div className="h-row"><span>state</span>{stateText(n)}</div>
      <div className="h-row"><span>status</span>{n.status} · <span>for</span>{ago(r.updated_at)}</div>
      <div className="h-row"><span>type</span>{r.type || "—"}</div>
      {n.status === "running" && <div className="h-row"><span>lease</span>{until(r.lease_expires_at)}</div>}
      {r.latest_run?.verdict && <div className="h-row"><span>verdict</span>{r.latest_run.verdict}</div>}
      <div className="h-row"><span>needs</span>{n.prereqs.length} · <span>unblocks</span>{n.children.length}</div>
      <div className="h-heat"><i style={{ width: `${Math.round(heat * 100)}%` }} /></div>
      <div className="h-note">click for details</div>
    </div>
  );
}

export function Drawer({ live, graphId, id, onSelect, onClose }: {
  live: Live; graphId: string; id: string; onSelect: (id: string) => void; onClose: () => void;
}) {
  useSyncExternalStore(live.subscribe, live.getVersion);
  useSecond();
  // Generated hooks. Their keys sit under /graphs/{graphId}/, so the page's
  // useGraphStream invalidates them whenever this graph changes — the drawer
  // stays current with no refresh logic of its own.
  const nodeQ = useGetNode(graphId, id);
  const runsQ = useListNodeRuns(graphId, id, { limit: 20 });
  const factsQ = useListNodeObservations(graphId, id, { limit: 20 });
  const detail = runsQ.data && factsQ.data
    ? {
        node: body<{ data: ApiNode }>(nodeQ.data)?.data,
        runs: body<{ data: ApiRun[] }>(runsQ.data)?.data ?? [],
        facts: body<{ data: ApiObservation[] }>(factsQ.data)?.data ?? [],
      }
    : null;
  const err = [nodeQ.error, runsQ.error, factsQ.error].find(Boolean)?.message ?? "";
  const n = live.nodes.get(id);

  useEffect(() => {
    const k = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    addEventListener("keydown", k);
    return () => removeEventListener("keydown", k);
  }, [onClose]);

  if (!n) return null;
  const node = detail?.node ?? n.raw;
  const chips = (ids: string[]) =>
    ids.length ? ids.map((x) => live.nodes.get(x)).filter(Boolean).map((m) => (
      <button key={m!.id} className={`chip ${shown(live, m!)}`} onClick={() => onSelect(m!.id)}>{m!.key}</button>
    )) : <span className="muted">none</span>;

  return (
    <aside className="drawer">
      <div className="d-head">
        <div>
          <div className="d-key">{n.key}</div>
          {node.title && node.title !== n.key && <div className="d-sub">{node.title}</div>}
        </div>
        <button className="x" onClick={onClose} aria-label="close">×</button>
      </div>
      <div className="d-status">
        <span className={`pill big ${shown(live, n)}`}>{stateText(n)}</span>
        <span className="muted">{n.status} for {ago(node.updated_at)}</span>
        {n.status === "running" && <span className="muted">· lease {until(node.lease_expires_at)}</span>}
      </div>
      {err && <div className="d-err">{err}</div>}

      <section>
        <h4>node</h4>
        <dl>
          <dt>type</dt><dd>{node.type || "—"}</dd>
          <dt>id</dt><dd className="mono">{n.id}</dd>
          <dt>created</dt><dd>{node.created_at ? new Date(node.created_at).toLocaleTimeString() : "—"} ({ago(node.created_at)} ago)</dd>
          <dt>lease</dt><dd>{node.lease_seconds ? `${node.lease_seconds}s` : "graph default"}</dd>
        </dl>
        {node.description && <p className="d-desc">{node.description}</p>}
      </section>

      <section>
        <h4>needs <span className="muted">({n.prereqs.length})</span></h4>
        <div className="chips">{chips(n.prereqs)}</div>
        <h4>unblocks <span className="muted">({n.children.length})</span></h4>
        <div className="chips">{chips(n.children)}</div>
      </section>

      <section>
        <h4>runs <span className="muted">({detail?.runs.length ?? "…"})</span></h4>
        {detail?.runs.length ? (
          <ol className="runs">
            {detail.runs.map((r) => (
              <li key={r.id}>
                <span className={`pill ${r.status === "succeeded" ? "done" : r.status === "running" ? "running" : r.status === "failed" ? "retry" : "waiting"}`}>
                  #{r.attempt}{r.epoch && r.epoch > 1 ? ` (epoch ${r.epoch})` : ""} {r.status}
                </span>
                <span className="muted">{dur(r.started_at, r.ended_at)}</span>
                {typeof r.data?.runner === "string" && <span className="muted">· {r.data.runner}</span>}
                {r.verification?.state && <span className="muted">· verdict {r.verification.state}</span>}
                {r.validation?.state && <span className="muted">· validation {r.validation.state}{r.validation.reason ? ` (${r.validation.reason})` : ""}</span>}
                {r.error && <div className="d-err">{r.error}</div>}
              </li>
            ))}
          </ol>
        ) : <span className="muted">{detail ? "never run" : "loading…"}</span>}
      </section>

      <section>
        <h4>facts <span className="muted">({detail?.facts.length ?? "…"})</span></h4>
        {detail?.facts.length ? (
          <ul className="facts">
            {detail.facts.map((f) => (
              <li key={f.id}>
                <div>{f.body}</div>
                <div className="muted">{f.source || "—"}{f.support ? ` · ${f.support}` : ""} · {ago(f.created_at)} ago</div>
              </li>
            ))}
          </ul>
        ) : <span className="muted">{detail ? "none recorded" : "loading…"}</span>}
      </section>

      <section>
        <h4>data</h4>
        <pre>{JSON.stringify(node.data ?? {}, null, 2)}</pre>
      </section>
    </aside>
  );
}
