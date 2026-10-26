// The demo registry. Each scenario lives in its own file; add one here.
import { ci } from "./ci";
import { fanIn } from "./fanin";
import { loop } from "./loop";
import { plan100 } from "./plan100";
import { reclaims } from "./reclaims";
import { recipe } from "./recipe";
import type { Scenario } from "./types";
import { validation } from "./validation";

export type { Scenario } from "./types";

export const SCENARIOS: Scenario[] = [plan100, recipe, ci, fanIn, reclaims, validation, loop];

export const scenarioById = (id: string) => SCENARIOS.find((s) => s.id === id);
