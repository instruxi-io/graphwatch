import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configure } from "@instruxi-io/graph-hooks";
import "@xyflow/react/dist/style.css";
import "./styles.css";
import { App } from "./App";

// One configure() for every graph hook, useGraphStream included. Here the base
// URL is this page's dev proxy, which adds the API key (vite.config.ts). A real
// app points at the gateway and passes getToken: () => <the user's session>.
configure({ baseUrl: `${location.origin}/api/v1/graph` });

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 2, refetchOnWindowFocus: false } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
