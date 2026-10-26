// Validation with the one identity this page has. A person or agent judge can
// never vote on a run it worked (self_judgment_forbidden), and here every run
// is worked by the same account, so the only judge that can sit is Jev: a
// 1-of-1 quorum, `judges.scope: none, jev: true` (dependents wait
// until Jev decides). Each completion carries evidence; Jev
// judges it asynchronously, so nodes show looking_for_validation before they
// finish. One job reports a failing check on its first attempt: Jev rejects
// it, the node goes back to looking_for_work, and the retry passes.
// A run Jev cannot decide escalates to an arbitrator — the owner, who here
// is also the worker, so the demo stops and says so rather than faking it.
import { putGraphValidation } from "@instruxi-io/graph-hooks";
import { timeout } from "../api";
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, type Scenario } from "./types";

const REJECT_FIRST = "tests";

const STEPS: [string, string, string[]][] = [
  ["spec", "write the spec", []],
  ["api", "implement the API", ["spec"]],
  ["ui", "implement the UI", ["spec"]],
  ["migration", "write the migration", ["spec"]],
  ["tests", "integration tests", ["api", "ui", "migration"]],
  ["docs", "update the docs", ["api"]],
  ["release", "cut the release", ["tests", "docs"]],
];

export const validation: Scenario = {
  id: "validation",
  title: "validated by Jev",
  description: "every completion is judged by Jev (1-of-1 quorum): nodes wait in looking_for_validation, one is rejected and retried",
  speed: 1,
  async build() {
    const id = await createDemoGraph("validation", "validated by Jev");
    await putGraphValidation(id, { quorum: { m: 1, n: 1 }, judges: { scope: "none" }, jev: true }, undefined, timeout());
    return id;
  },
  async drive(d) {
    const nodes: NodeSpec[] = STEPS.map(([key, title, prereqs]) => ({
      key, title, prereqs,
      // One criterion an exit code settles. A softer second one ("the output
      // reports it complete") left a failing run inside Jev's uncertain band:
      // every sample rejected, yet the aggregate came back undecided and the
      // run escalated to an arbitrator this page cannot be.
      data: { acceptance: [`The ${key} check command exited 0`] },
    }));
    await d.addNodes(nodes);
    const work = async (c: { key: string; attempt?: number }) => {
      await d.sleep(jitter(2500));
      const fail = c.key === REJECT_FIRST && (c.attempt ?? 1) === 1;
      // The run SUCCEEDS either way; it is the evidence Jev weighs.
      return {
        status: "succeeded" as const,
        evidence: [{
          kind: "command" as const, cmd: `./check ${c.key}`, exit: fail ? 1 : 0,
          output: fail ? `${c.key}: 2 of 40 assertions failed\nexit status 1\n` : `${c.key}: all checks passed\nexit status 0\n`,
        }],
      };
    };
    await Promise.all(["worker-1", "worker-2"].map((r) => d.worker(r, work)));
    if (d.stalled) d.say(`stopped: ${d.stalled}`, "#fac850");
  },
};
