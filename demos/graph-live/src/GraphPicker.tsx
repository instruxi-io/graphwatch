import { useEffect, useRef, useState } from "react";
import { useListGraphs } from "@instruxi-io/graph-hooks";
import { body } from "./api";
import { isDemoSlug } from "./demo/naming";

type GraphRow = {
  id: string;
  name: string;
  slug: string;
  mode: string;
  node_count: number;
  edge_count: number;
  role?: string;
  created_at: string;
};

const PAGE = 50;

function ago(iso: string) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

/**
 * Pick an existing graph to WATCH. Nothing here drives it: the page only
 * reads and listens, so whatever harnesses are working the graph keep doing
 * so and the picture follows them.
 */
export function GraphPicker({ current, title, onPick, hideDemos, setHideDemos }: {
  current: string; title: string; onPick: (id: string) => void; hideDemos: boolean; setHideDemos: (b: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState(PAGE);
  const box = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);

  // Debounce the search box into the query, and start again from one page.
  useEffect(() => {
    const t = setTimeout(() => { setQuery(q.trim()); setLimit(PAGE); }, 200);
    return () => clearTimeout(t);
  }, [q]);

  // The generated list hook; fetched only while the panel is open. "load
  // more" widens the page (the API caps a page at 200).
  const list = useListGraphs({ limit, ...(query ? { q: query } : {}) }, { query: { enabled: open } });
  const page = body<{ data: GraphRow[]; meta: { total: number } }>(list.data);
  const all = page?.data ?? [];
  // Demo graphs are named demo-<scenario>-…; hiding them is a filter over the
  // page that was read, so "n of total" still counts what the API returned.
  const rows = hideDemos ? all.filter((g) => !isDemoSlug(g.slug)) : all;
  const hidden = all.length - rows.length;
  const total = page?.meta.total ?? 0;
  const loading = list.isFetching;
  const err = list.error?.message ?? "";

  useEffect(() => {
    if (!open) return;
    input.current?.focus();
    const away = (e: MouseEvent) => { if (!box.current?.contains(e.target as Node)) setOpen(false); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    addEventListener("mousedown", away);
    addEventListener("keydown", esc);
    return () => { removeEventListener("mousedown", away); removeEventListener("keydown", esc); };
  }, [open]);

  return (
    <div className="picker" ref={box}>
      <button className="pick" onClick={() => setOpen((o) => !o)} title="watch an existing graph">
        <span className="muted">graph</span> {title || (current ? "…" : "choose a graph")} <span className="caret">▾</span>
      </button>
      {open && (
        <div className="pick-panel">
          <input ref={input} className="pick-search" placeholder="search by name…" value={q} onChange={(e) => setQ(e.target.value)} />
          <label className="pick-toggle">
            <input type="checkbox" checked={hideDemos} onChange={(e) => setHideDemos(e.target.checked)} /> hide demo graphs
          </label>
          {err && <div className="d-err">{err}</div>}
          <ul>
            {rows.map((g) => (
              <li key={g.id}>
                <button className={`pick-row${g.id === current ? " on" : ""}`} onClick={() => { onPick(g.id); setOpen(false); }}>
                  <span className="pick-name">{g.name}</span>
                  <span className="pick-meta">
                    {g.slug} · {g.node_count} nodes · {g.edge_count} edges{g.mode !== "dag" ? ` · ${g.mode}` : ""}
                    {g.role ? ` · ${g.role}` : ""} · {ago(g.created_at)}
                  </span>
                </button>
              </li>
            ))}
            {!rows.length && !loading && <li className="muted pick-empty">no graphs{q ? ` matching “${q}”` : ""}</li>}
          </ul>
          <div className="pick-foot">
            <span className="muted">{all.length} of {total}{hidden ? ` · ${hidden} demo${hidden === 1 ? "" : "s"} hidden` : ""}</span>
            {all.length < total && limit < 200 && (
              <button className="pick-more" disabled={loading || limit >= 200} onClick={() => setLimit((l) => Math.min(200, l + PAGE))}>
                {loading ? "loading…" : "load more"}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
