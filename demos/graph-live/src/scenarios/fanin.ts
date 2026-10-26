// Wide fan-out, one join: 24 shards run in parallel and a single merge waits
// on all of them, so one slow shard IS the critical path (the header shows the
// summary's critical_path.remaining shrinking).
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, type Scenario } from "./types";

const SHARDS = 24;
const SLOW = "shard_17";

export const fanIn: Scenario = {
  id: "fan-in",
  title: "fan-out, fan-in",
  description: "one split, 24 parallel shards, a single merge: eight workers, and one slow shard holds up the join",
  speed: 1,
  async build() {
    return createDemoGraph("fan-in", "fan-out, fan-in");
  },
  async drive(d) {
    const shards = Array.from({ length: SHARDS }, (_, i) => `shard_${String(i + 1).padStart(2, "0")}`);
    const nodes: NodeSpec[] = [
      { key: "split", title: "split the input" },
      ...shards.map((k) => ({ key: k, title: k.replace("_", " "), prereqs: ["split"] })),
      { key: "merge", title: "merge every shard", prereqs: shards },
      { key: "publish", title: "publish", prereqs: ["merge"] },
    ];
    await d.addNodes(nodes);
    const work = async (c: { key: string }) => {
      await d.sleep(c.key === SLOW ? 14000 : c.key.startsWith("shard") ? jitter(3000, 0.6) : 1500);
      return { status: "succeeded" as const };
    };
    await Promise.all(Array.from({ length: 8 }, (_, w) => d.worker(`demo-worker-${w + 1}`, work)));
  },
};
