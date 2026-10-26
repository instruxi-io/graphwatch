// ELK's layered algorithm (the Sugiyama family): top to bottom, prerequisites
// above what they unblock, crossings minimised. Runs only when the SET of
// nodes or edges changes, never on a status change.
import ELK from "elkjs/lib/elk.bundled.js";
import type { LiveNode } from "./live";

const elk = new ELK();
export const NODE_W = 96;
export const NODE_H = 46;

export async function layout(nodes: LiveNode[]): Promise<Map<string, { x: number; y: number }>> {
  const ids = new Set(nodes.map((n) => n.id));
  const res = await elk.layout({
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "DOWN",
      "elk.spacing.nodeNode": "18",
      "elk.layered.spacing.nodeNodeBetweenLayers": "62",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
    },
    children: [...nodes].sort((a, b) => a.created - b.created).map((n) => ({ id: n.id, width: NODE_W, height: NODE_H })),
    edges: nodes.flatMap((n) =>
      n.prereqs.filter((p) => ids.has(p)).map((p) => ({ id: `${p}->${n.id}`, sources: [p], targets: [n.id] })),
    ),
  });
  return new Map((res.children ?? []).map((c) => [c.id, { x: c.x ?? 0, y: c.y ?? 0 }]));
}
