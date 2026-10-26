import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The page talks to the real graph API through this proxy, for two reasons:
// the browser needs no CORS allowance from api.instruxi.dev, and the API key
// stays in this process — it is added to each proxied request here and never
// reaches the bundle or the browser.
const target = process.env.GRAPH_BASE_URL ?? "https://api.instruxi.dev";
const key = process.env.GRAPH_API_KEY ?? "";

export default defineConfig(({ command }) => {
  // Only the dev server proxies, so only it needs the key; `npm run build`
  // bundles without one (and the bundle never contains it either way).
  if (command === "serve" && !key) throw new Error("graph-live: set GRAPH_API_KEY (see README.md)");
  return {
    plugins: [react()],
    // hooks-shared holds the configure() registry and must be ONE instance
    // (two copies would each hold their own configuration); React and TanStack Query likewise.
    resolve: { dedupe: ["react", "react-dom", "@tanstack/react-query", "@instruxi-io/hooks-shared"] },
    server: {
      port: 5178,
      // GRAPH_LIVE_POLL=1 watches by polling instead of inotify — for a machine
      // whose watcher limit is already spent (ENOSPC at startup).
      watch: process.env.GRAPH_LIVE_POLL === "1" ? { usePolling: true, interval: 500 } : undefined,
      proxy: {
        "/api/v1/graph": {
          target,
          changeOrigin: true,
          headers: { "X-API-Key": key },
          // GRAPH_LIVE_LOG=1 prints one line per proxied request (method and
          // path, never the key): the way to confirm that Watch mode only ever
          // issues GETs and the stream.
          configure: (proxy) => {
            if (process.env.GRAPH_LIVE_LOG !== "1") return;
            proxy.on("proxyReq", (_req, req) => console.log(`[graph-live] ${req.method} ${req.url}`));
          },
        },
      },
    },
  };
});
