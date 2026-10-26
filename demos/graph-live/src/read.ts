// Reading a WHOLE graph, however large, through the generated functions.
//
// Nodes come from GET /nodes/query (queryNodes), because it is the list that
// pages with an opaque keyset cursor: pass each page's `next` back as
// `after` until there is none. Keyset paging is exact under concurrent writes,
// which offset paging is not (a row inserted before the offset shifts every
// later page by one). Sorted by created_at, which never changes, so a node
// updated mid-read cannot jump pages. Each item carries the server's
// work_state, reclaimable and opens_at.
//
// Edges have no cursor list, so they page by offset against meta.total.
import { listEdges, queryNodes } from "@instruxi-io/graph-hooks";
import { body, type ApiEdge, type ApiNode } from "./api";

const PAGE = 200; // the API's maximum page
const MAX_PAGES = 100; // 20,000 rows: past this the read says it is partial

export type NodesRead = { nodes: ApiNode[]; total: number; complete: boolean; pages: number };
export type EdgesRead = { edges: ApiEdge[]; total: number; complete: boolean };

export async function readAllNodes(graphId: string, signal?: AbortSignal): Promise<NodesRead> {
  const nodes: ApiNode[] = [];
  let after: string | undefined;
  let total = 0;
  let pages = 0;
  do {
    const r = body<{ data: { items: ApiNode[]; total: number; next?: string } }>(
      await queryNodes({ graph_id: graphId, sort: "created_at", limit: PAGE, ...(after ? { after } : {}) }, { signal }),
    )!.data;
    nodes.push(...(r.items ?? []));
    total = r.total ?? nodes.length;
    after = r.next || undefined;
    pages++;
  } while (after && pages < MAX_PAGES);
  return { nodes, total, complete: !after, pages };
}

export async function readAllEdges(graphId: string, signal?: AbortSignal): Promise<EdgesRead> {
  const edges: ApiEdge[] = [];
  let total = 0;
  for (let page = 0; page < MAX_PAGES; page++) {
    const r = body<{ data: ApiEdge[]; meta: { total: number } }>(
      await listEdges(graphId, { limit: PAGE, offset: page * PAGE }, { signal }),
    )!;
    edges.push(...r.data);
    total = r.meta.total;
    if (r.data.length < PAGE || edges.length >= total) break;
  }
  return { edges, total, complete: edges.length >= total };
}

/**
 * Query keys for the page's two whole-graph reads. They sit under
 * `/graphs/{id}/`, so useGraphStream's invalidation (which matches every key
 * under that prefix) refetches them on every event, exactly as it does the
 * generated hooks' own keys.
 */
export const nodesKey = (graphId: string) => [`/graphs/${graphId}/nodes`, { all: "query-cursor" }] as const;
export const edgesKey = (graphId: string) => [`/graphs/${graphId}/edges`, { all: "offset" }] as const;
