/**
 * Every demo graph is named `demo-<scenario>-…` (older plays: `graph-live-…`),
 * so Watch's picker can tell demos from real graphs without importing the
 * driver: watching never loads the code that writes.
 */
export const DEMO_PREFIXES = ["demo-", "graph-live-"];
export const isDemoSlug = (slug: string) => DEMO_PREFIXES.some((p) => slug.startsWith(p));
