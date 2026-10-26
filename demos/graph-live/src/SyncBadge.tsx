import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { GraphStreamStatus } from "@instruxi-io/graph-hooks";
import type { Live } from "./live";
import { emptyReport, verify, type SyncReport } from "./sync";

const VERIFY_EVERY = 20_000; // ms between verifications
const RECHECK_AFTER = 3_000; // a suspected drift is rechecked this soon
const QUIET = 1_500; // no event for this long before a check starts
const CATCHING_UP = 800; // an event this much newer than the read is "catching up"
const BEHIND = 5_000; // ...and this much is "behind"

function ago(ms: number) {
  if (!ms) return "never";
  const s = Math.round((Date.now() - ms) / 1000);
  return s < 60 ? `${s}s ago` : `${Math.round(s / 60)}m ago`;
}

/** Runs the checks and folds them into one state. */
function useSync(live: Live, graphId: string, isDag: boolean, stream: GraphStreamStatus): SyncReport {
  useSyncExternalStore(live.subscribe, live.getVersion);
  const [report, setReport] = useState<SyncReport>(emptyReport);
  const suspect = useRef(0); // consecutive failed verifications
  const next = useRef(Date.now() + 3000); // first check shortly after load
  const running = useRef(false);
  const [, tick] = useState(0);

  useEffect(() => {
    setReport(emptyReport());
    suspect.current = 0;
    next.current = Date.now() + 3000;
  }, [graphId]);

  useEffect(() => {
    const t = setInterval(async () => {
      tick((x) => x + 1); // freshness is time-based: re-evaluate every second
      const now = Date.now();
      const quiet = now - live.lastEventAt > QUIET && live.readAt >= live.lastEventAt;
      if (running.current || now < next.current || !quiet || !live.nodes.size) return;
      running.current = true;
      const v = await verify(live, graphId, isDag);
      running.current = false;
      if ("inconclusive" in v) {
        next.current = Date.now() + RECHECK_AFTER;
        setReport((r) => ({ ...r, note: v.inconclusive }));
        return;
      }
      suspect.current = v.ok ? 0 : suspect.current + 1;
      next.current = Date.now() + (v.ok ? VERIFY_EVERY : RECHECK_AFTER);
      setReport((r) => ({ ...r, ...v.report, note: v.ok ? "" : suspect.current < 2 ? "mismatch seen once; rechecking" : "" }));
    }, 1000);
    return () => clearInterval(t);
  }, [live, graphId, isDag]);

  // Fold: a confirmed drift or a dead stream wins; otherwise freshness.
  const gap = live.lastEventAt - live.readAt;
  let state: SyncReport["state"];
  if (stream === "error" || stream === "revoked") state = "behind";
  else if (suspect.current >= 2) state = "drift";
  else if (!report.checkedAt && !live.readAt) state = "verifying";
  else if (gap > BEHIND) state = "behind";
  else if (gap > CATCHING_UP) state = "catching-up";
  else state = "in-sync";
  return { ...report, state };
}

const LABEL: Record<SyncReport["state"], string> = {
  verifying: "… verifying",
  "in-sync": "✓ in sync",
  "catching-up": "↻ catching up",
  behind: "⚠ behind",
  drift: "⚠ drift",
};

export function SyncBadge({ live, graphId, isDag, stream }: {
  live: Live; graphId: string; isDag: boolean; stream: GraphStreamStatus;
}) {
  const r = useSync(live, graphId, isDag, stream);
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => { if (!box.current?.contains(e.target as Node)) setOpen(false); };
    addEventListener("mousedown", away);
    return () => removeEventListener("mousedown", away);
  }, [open]);

  const warn = r.reclaimable.length > 0 || r.missingLeases > 0 || r.escalated > 0 || r.partial !== null;
  return (
    <div className="sync" ref={box}>
      <button className={`sync-badge ${r.state}`} onClick={() => setOpen((o) => !o)} title="is the page showing the server's state?">
        {LABEL[r.state]}{warn && <span className="sync-warn" title="warnings">●</span>}
      </button>
      {open && (
        <div className="sync-panel">
          <div className="sync-row"><span>stream</span>{stream}{live.seq ? ` · seq ${live.seq}` : ""}</div>
          <div className="sync-row"><span>last event</span>{ago(live.lastEventAt)}</div>
          <div className="sync-row"><span>last read</span>{ago(live.readAt)}</div>
          <div className="sync-row">
            <span>verified</span>{ago(r.checkedAt)}{r.checkedAt ? ` · ${r.checkedNodes} nodes compared` : ""}
          </div>
          {r.summary && (
            <div className="sync-row">
              <span>summary</span>
              {r.summary.agree ? `✓ read and summary agree (${r.summary.live} live)` : `✗ ${r.summary.diffs.length} difference(s)`}
            </div>
          )}
          {r.checkedAt > 0 && <div className="sync-row"><span>paged</span>{r.pages} page{r.pages === 1 ? "" : "s"} by cursor</div>}
          {!isDag && <div className="sync-row"><span>work state</span>not defined for a cyclic graph; status only</div>}
          {r.note && <div className="sync-note">{r.note}</div>}
          {r.mismatches.length > 0 && (
            <div className="sync-list">
              <h5>status mismatches</h5>
              {r.mismatches.slice(0, 8).map((m) => (
                <div key={m.key}>{m.key}: drawn <b>{m.drawn}</b>, server <b>{m.server}</b></div>
              ))}
            </div>
          )}
          {r.summary && !r.summary.agree && (
            <div className="sync-list">
              <h5>read vs summary</h5>
              {r.summary.diffs.slice(0, 8).map((d) => <div key={d}>{d}</div>)}
            </div>
          )}
          {r.clock.length > 0 && (
            <div className="sync-list">
              <h5>moved by the clock ({r.clock.length})</h5>
              <div>{r.clock.slice(0, 10).join(", ")} — a lease lapsed or a gate opened since the last read; not drift, and the page rereads at that instant.</div>
            </div>
          )}
          {r.reclaimable.length > 0 && (
            <div className="sync-list warn">
              <h5>reclaimable ({r.reclaimable.length})</h5>
              <div>{r.reclaimable.slice(0, 10).join(", ")} — running past their lease; the worker is presumed gone and the next claim reclaims it.</div>
            </div>
          )}
          {r.missingLeases > 0 && (
            <div className="sync-list warn">
              <h5>no lease ({r.missingLeases})</h5>
              <div>running with no lease at all: never reclaimed, a silent stall.</div>
            </div>
          )}
          {r.escalated > 0 && (
            <div className="sync-list warn">
              <h5>escalated ({r.escalated})</h5>
              <div>validation could not decide; an arbitrator (the owner or a tenant admin, never the run's worker) must.</div>
            </div>
          )}
          {r.partial && (
            <div className="sync-list warn">
              <h5>partial view</h5>
              <div>read {r.partial.read} of {r.partial.total} nodes before the page cap.</div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
