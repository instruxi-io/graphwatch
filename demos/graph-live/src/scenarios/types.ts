import type { Demo } from "../demo/driver";

export type BuildOptions = { signal: AbortSignal; size?: number };

/**
 * One demo. build() creates its graph (named `demo-<id>-…`) and returns the
 * id; drive() works it until it is done or the demo is stopped. Both go
 * through the published hooks only (demo/driver.ts).
 */
export interface Scenario {
  id: string;
  title: string;
  description: string;
  /** The speed the scenario reads best at; the header's control overrides it. */
  speed?: number;
  build(o: BuildOptions): Promise<string>;
  drive(d: Demo, o: BuildOptions): Promise<void>;
}

export const jitter = (ms: number, spread = 0.3) => ms * (1 - spread + Math.random() * spread * 2);
export const pick = <T,>(a: T[]) => a[Math.floor(Math.random() * a.length)];
