// The original ▶ play: a planner grows the DAG in bursts while six workers
// claim from the frontier and complete what they claim.
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, pick, type Scenario } from "./types";

const WORDS = [
  "parse", "lint", "compile", "bundle", "index", "embed", "rank", "fetch", "verify", "sign",
  "migrate", "seed", "cache", "shard", "render", "probe", "trace", "audit", "pack", "tune",
];

export const plan100: Scenario = {
  id: "plan-100",
  title: "growing plan",
  description: "a planner grows a 100-node plan in bursts while six workers claim and complete it; ~7% of runs fail and retry",
  speed: 1,
  async build() {
    return createDemoGraph("plan-100", "growing plan");
  },
  async drive(d, o) {
    const size = o.size ?? 100;
    await d.addNodes([{ key: "plan", title: "plan" }]);
    const keys: string[] = ["plan"];
    const children = new Map<string, number>();
    let planned = false;

    const planner = async () => {
      let i = 1;
      while (keys.length < size - 1 && !d.stopped) {
        const n = keys.length;
        const parent = keys[Math.max(0, n - 1 - Math.floor(Math.random() * Math.min(n, 8)))];
        const burst = Math.min(1 + Math.floor(Math.random() * 4), size - 1 - n);
        const items: NodeSpec[] = Array.from({ length: burst }, () => {
          const key = `${pick(WORDS)}-${i++}`;
          const prereqs = [parent];
          if (n > 5 && Math.random() < 0.3) {
            // Join a RECENT branch: a join to a node from long ago is an edge
            // across the whole picture.
            const other = keys[Math.max(0, n - 1 - Math.floor(Math.random() * Math.min(n, 14)))];
            if (other !== parent) prereqs.push(other);
          }
          return { key, prereqs };
        });
        try {
          await d.addNodes(items);
          for (const it of items) {
            keys.push(it.key);
            for (const p of it.prereqs ?? []) children.set(p, (children.get(p) ?? 0) + 1);
          }
        } catch (e) {
          // Skip the burst and carry on: a node left `cancelled` is harmless.
          d.say(`planner: skipped a burst — ${(e as Error).message}`, "#f05040");
        }
        await d.sleep(500 + Math.random() * 900);
      }
      if (d.stopped) return;
      // Close the plan on its newest few leaves. The plan completes only when
      // EVERY node is done (graph_is_complete), so ship needs no edge to the rest.
      const leaves = keys.filter((k) => !children.get(k)).slice(-5);
      await d.addNodes([{ key: "ship", prereqs: leaves }]).catch((e) => d.say(`planner: ${e.message}`, "#f05040"));
      planned = true;
    };

    const work = async () => {
      await d.sleep(jitter(2200, 0.6));
      return Math.random() < 0.07
        ? { status: "failed" as const, error: "simulated failure" }
        : { status: "succeeded" as const };
    };
    await Promise.all([planner(), ...Array.from({ length: 6 }, (_, w) => d.worker(`demo-worker-${w + 1}`, work, () => !planned))]);
  },
};
