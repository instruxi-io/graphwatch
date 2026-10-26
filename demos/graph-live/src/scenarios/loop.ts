// Epochs: a loop is RUNS scaled to a graph, never an edge: a graph has no cycles. A small
// pipeline runs to completion, then POST /graphs/{id}/reset starts it over in
// place as the next epoch — every node back to active, every run kept as
// history — three times. The header shows the epoch.
import { getGraphSummary, resetGraph } from "@instruxi-io/graph-hooks";
import { body, codeOf, timeout, type ApiSummary } from "../api";
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, type Scenario } from "./types";

const EPOCHS = 3;

export const loop: Scenario = {
  id: "loop",
  title: "epochs (reset)",
  description: "a five-node pipeline runs to completion, is reset in place as the next epoch, and runs again — three epochs",
  speed: 1,
  async build() {
    return createDemoGraph("loop", "epochs");
  },
  async drive(d) {
    const nodes: NodeSpec[] = [
      { key: "fetch", title: "fetch the feed" },
      { key: "parse", title: "parse", prereqs: ["fetch"] },
      { key: "score", title: "score", prereqs: ["parse"] },
      { key: "dedupe", title: "dedupe", prereqs: ["parse"] },
      { key: "report", title: "report", prereqs: ["score", "dedupe"] },
    ];
    await d.addNodes(nodes);
    const work = async () => {
      await d.sleep(jitter(1800));
      return { status: "succeeded" as const };
    };
    for (let e = 1; e <= EPOCHS && !d.stopped; e++) {
      d.complete = false;
      await Promise.all(["worker-1", "worker-2"].map((r) => d.worker(r, work)));
      if (d.stopped || e === EPOCHS) break;
      await d.sleep(2500);
      const s = body<{ data: ApiSummary }>(await getGraphSummary(d.graphId, undefined, timeout()))!.data;
      try {
        // expected_epoch is the idempotency guard: a retry after a lost reply
        // gets 409 epoch_mismatch, which means it already happened.
        await resetGraph(d.graphId, { expected_epoch: s.epoch, reason: "demo loop" }, timeout());
      } catch (err) {
        if (codeOf(err) !== "epoch_mismatch") throw err;
      }
      d.say(`reset: epoch ${(s.epoch ?? e) + 1} begins`, "#7cc4fa");
    }
  },
};
