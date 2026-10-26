// A CI pipeline: lint and test fan out per package, each package builds on
// both, the images package all builds, then staging → smoke → production.
// Two jobs fail on their first attempt (a flaky test, a lint slip) and retry.
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, type Scenario } from "./types";

const PACKAGES = ["api", "web", "worker", "sdk"];
const FLAKY = new Set(["test_web", "lint_sdk"]); // fail attempt 1, pass attempt 2
const SECONDS: Record<string, number> = { checkout: 1, lint: 1.5, test: 3.5, build: 2.5, images: 3, deploy: 3, smoke: 2 };

export const ci: Scenario = {
  id: "ci-pipeline",
  title: "CI pipeline",
  description: "lint/test fan-out per package → build → images → staging → smoke → production; two jobs fail once and retry",
  speed: 1,
  async build() {
    return createDemoGraph("ci-pipeline", "CI pipeline");
  },
  async drive(d) {
    const nodes: NodeSpec[] = [{ key: "checkout", title: "git checkout", type: "checkout" }];
    for (const p of PACKAGES) {
      nodes.push({ key: `lint_${p}`, title: `lint ${p}`, type: "lint", prereqs: ["checkout"] });
      nodes.push({ key: `test_${p}`, title: `test ${p}`, type: "test", prereqs: ["checkout"] });
      nodes.push({ key: `build_${p}`, title: `build ${p}`, type: "build", prereqs: [`lint_${p}`, `test_${p}`] });
    }
    nodes.push({ key: "images", title: "build and push images", type: "images", prereqs: PACKAGES.map((p) => `build_${p}`) });
    nodes.push({ key: "deploy_staging", title: "deploy to staging", type: "deploy", prereqs: ["images"] });
    nodes.push({ key: "smoke_staging", title: "smoke tests on staging", type: "smoke", prereqs: ["deploy_staging"] });
    nodes.push({ key: "deploy_prod", title: "deploy to production", type: "deploy", prereqs: ["smoke_staging"] });
    await d.addNodes(nodes);

    const kind = (key: string) => key.split("_")[0];
    const work = async (c: { key: string; attempt?: number }) => {
      await d.sleep(jitter((SECONDS[kind(c.key)] ?? 2) * 1000));
      if (FLAKY.has(c.key) && (c.attempt ?? 1) === 1) {
        return { status: "failed" as const, error: c.key.startsWith("test") ? "1 flaky test failed" : "lint: 2 problems" };
      }
      return { status: "succeeded" as const };
    };
    await Promise.all(["runner-a", "runner-b", "runner-c", "runner-d"].map((r) => d.worker(r, work)));
  },
};
