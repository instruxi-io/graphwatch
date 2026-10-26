// Workers that crash mid-run. A crashed worker walks away holding its claim:
// no report, no heartbeat. Its node stays `claimed` until the lease lapses
// (30 s of real time, the API's minimum, whatever the demo speed), then the
// server calls it looking_for_work, reclaimable, and the next claim takes it
// over. Two long jobs heartbeat to keep their lease alive past 30 s.
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, type Scenario } from "./types";

const CRASH_ON_FIRST = new Set(["extract_b", "transform_c", "load"]);
const LONG = new Set(["transform_a", "transform_d"]); // longer than a lease: heartbeats

export const reclaims: Scenario = {
  id: "failures-and-reclaims",
  title: "crashes and reclaims",
  description: "three workers crash mid-run; leases (30 s) lapse, the nodes turn reclaimable and are taken over; two long jobs heartbeat",
  speed: 1,
  async build() {
    return createDemoGraph("failures-and-reclaims", "crashes and reclaims", { default_lease_seconds: 30 });
  },
  async drive(d) {
    const parts = ["a", "b", "c", "d"];
    const nodes: NodeSpec[] = [
      { key: "plan", title: "plan the batch" },
      ...parts.map((p) => ({ key: `extract_${p}`, title: `extract ${p}`, prereqs: ["plan"] })),
      ...parts.map((p) => ({ key: `transform_${p}`, title: `transform ${p}`, prereqs: [`extract_${p}`] })),
      { key: "load", title: "load everything", prereqs: parts.map((p) => `transform_${p}`) },
      { key: "report", title: "report", prereqs: ["load"] },
    ];
    await d.addNodes(nodes);
    const work = async (c: { key: string; attempt?: number; node_id: string; run_id: string }) => {
      if (CRASH_ON_FIRST.has(c.key) && (c.attempt ?? 1) === 1) {
        await d.sleep(1500);
        return "crash" as const;
      }
      if (LONG.has(c.key)) {
        d.say(`${c.key}: a long job — heartbeating to keep its lease`, "#7c86a8");
        await d.hold(c, 40_000 * (0.9 + Math.random() * 0.2));
      } else {
        await d.sleep(jitter(2500));
      }
      return { status: "succeeded" as const };
    };
    // Idle workers poll every ~5 s, so a node that turns reclaimable stays
    // drawn that way for a few seconds before someone takes it over.
    await Promise.all(["worker-1", "worker-2", "worker-3", "worker-4"].map((r) => d.worker(r, work, undefined, 5000)));
  },
};
