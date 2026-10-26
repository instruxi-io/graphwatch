// Shared shapes and small helpers. Every call — reads AND the demo driver's
// writes — goes through the published hooks' generated functions
// (@instruxi-io/graph-hooks); there is no hand-written fetch in this app.
import { HttpError } from "@instruxi-io/graph-hooks";

/**
 * The generated functions resolve to the response BODY, though their types
 * describe an envelope around it that the server does not send.
 * One cast, here, so call sites read the body as what it is.
 */
export const body = <T,>(r: unknown): T | undefined => r as T | undefined;

/**
 * RequestInit for a driver write: a request that never answers must fail, not
 * hang. A planner awaiting one forever lets the workers finish what exists and
 * the plan "completes" at whatever size it had reached.
 */
export const timeout = (ms = 20000): RequestInit => ({ signal: AbortSignal.timeout(ms) });

/** Status of a failed call, or 0 when the request never got a response. */
export const statusOf = (e: unknown) => (e instanceof HttpError ? e.status : 0);

/** The machine code of a failed call (`{"error": "<code>"}`), or "". */
export const codeOf = (e: unknown) =>
  e instanceof HttpError && e.data && typeof e.data === "object" ? String((e.data as { error?: unknown }).error ?? "") : "";

/**
 * Retries a call that is SAFE to repeat — an upsert, a status set, a completion
 * reporting the same outcome — on a network failure or a 5xx. Never wrap a
 * claim: a claim whose reply was lost has already taken the node.
 */
export async function retry<T>(fn: () => Promise<T>, attempts = 6): Promise<T> {
  for (let i = 0; ; i++) {
    try {
      return await fn();
    } catch (e) {
      const status = statusOf(e);
      if (i + 1 >= attempts || (status !== 0 && status < 500)) throw e;
      await new Promise((r) => setTimeout(r, 400 * 2 ** i));
    }
  }
}

/**
 * A node's work state as the SERVER derives it (graph_node_work, migrations
 * 050/066): the one rule the claim, the frontier and the summary all read.
 * The page draws this and never re-derives it from status and edges.
 */
export type WorkState =
  | "looking_for_work"
  | "claimed"
  | "looking_for_validation"
  | "looking_for_arbitration"
  | "not_yet_available"
  | "finished";

export const WORK_STATES: WorkState[] = [
  "looking_for_work", "claimed", "looking_for_validation", "looking_for_arbitration", "not_yet_available", "finished",
];

export type ApiNode = {
  id: string;
  key: string;
  status: string;
  /** Absent on a graph-mode (cyclic) graph, where the frontier is undefined. */
  work_state?: WorkState | null;
  /** A running node whose lease lapsed: looking_for_work, and the next claim reclaims it. */
  reclaimable?: boolean | null;
  /** Prerequisites met, but time-gated until then (not_yet_available). */
  opens_at?: string | null;
  type?: string;
  title?: string;
  description?: string;
  data?: Record<string, unknown>;
  lease_expires_at?: string | null;
  lease_seconds?: number | null;
  epoch?: number;
  latest_run?: { id: string; attempt: number; status: string; verdict?: string | null; runner?: string | null } | null;
  created_at?: string;
  updated_at?: string;
};
export type ApiRun = {
  id: string;
  attempt: number;
  epoch?: number;
  status: string;
  started_at: string;
  ended_at?: string | null;
  error?: string | null;
  data?: Record<string, unknown>;
  verification?: { state?: string; reason?: string } | null;
  validation?: { state?: string; reason?: string } | null;
};
export type ApiObservation = {
  id: string;
  body: string;
  source?: string;
  support?: string | null;
  created_at: string;
  archived_at?: string | null;
};
export type ApiEdge = { from_node_id: string; to_node_id: string; type: string };
export type ApiGraph = {
  id: string; slug: string; name: string; dependency_edge_type: string; mode?: string; epoch?: number;
};
/** The subset of GET /graphs/{id}/summary this page reads (counts, and the lists of reclaimable and clock-gated nodes). */
export type ApiSummary = {
  state?: string;
  epoch?: number;
  complete?: boolean;
  nodes?: { live?: number; archived?: number };
  by_work_state?: Partial<Record<WorkState, number>>;
  demand?: { reclaimable?: number; frontier_size?: number };
  leases?: { missing?: number; expired?: number };
  validation?: { pending?: number; escalated?: number; judges_wanted?: number };
  critical_path?: { remaining?: number; bounded?: boolean };
  as_of?: string;
};
