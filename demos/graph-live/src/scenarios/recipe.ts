// A cassoulet: parallel confit / beans / pork / sausage branches join at
// assembly, then three sequential bakes. Human-readable, worked by three cooks
// at a watchable pace.
import { createDemoGraph, type NodeSpec } from "../demo/driver";
import { jitter, type Scenario } from "./types";

// [key, title, seconds at 1×, prerequisites]
const STEPS: [string, string, number, string[]][] = [
  ["soak_beans", "Soak the tarbais beans overnight", 6, []],
  ["cure_duck", "Salt-cure the duck legs", 5, []],
  ["brown_pork", "Brown the pork shoulder", 3, []],
  ["brown_sausage", "Brown the Toulouse sausages", 2.5, []],
  ["render_rind", "Render the pork rind", 3, []],
  ["make_crumbs", "Make the breadcrumbs", 1.5, []],
  ["confit_duck", "Confit the duck legs in their fat", 8, ["cure_duck"]],
  ["simmer_beans", "Simmer the beans with aromatics", 6, ["soak_beans"]],
  ["braise_pork", "Braise the pork with tomato and garlic", 7, ["brown_pork"]],
  ["stock", "Make the stock from the rind and bones", 4, ["render_rind"]],
  ["rub_cassole", "Rub the cassole with garlic", 1, []],
  ["assemble", "Layer beans and meats in the cassole", 3, ["confit_duck", "simmer_beans", "braise_pork", "brown_sausage", "stock", "rub_cassole"]],
  ["bake_1", "First bake; break the crust", 6, ["assemble"]],
  ["bake_2", "Second bake; break the crust again", 6, ["bake_1"]],
  ["bake_3", "Last bake under breadcrumbs", 6, ["bake_2", "make_crumbs"]],
  ["rest", "Rest before serving", 2, ["bake_3"]],
  ["serve", "Serve", 1, ["rest"]],
];

export const recipe: Scenario = {
  id: "recipe",
  title: "cassoulet",
  description: "a fixed recipe DAG: four branches join at assembly, then three bakes in sequence; three cooks, no failures",
  speed: 1,
  async build() {
    return createDemoGraph("recipe", "cassoulet");
  },
  async drive(d) {
    const nodes: NodeSpec[] = STEPS.map(([key, title, secs, prereqs]) => ({ key, title, prereqs, data: { seconds: secs } }));
    await d.addNodes(nodes);
    const secs = new Map(STEPS.map(([k, , s]) => [k, s]));
    const work = async (c: { key: string }) => {
      await d.sleep(jitter((secs.get(c.key) ?? 2) * 1000, 0.15));
      return { status: "succeeded" as const };
    };
    await Promise.all(["chef", "sous-chef", "commis"].map((r) => d.worker(r, work)));
  },
};
