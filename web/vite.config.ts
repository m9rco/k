import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Build output goes to web/static so the Go binary's existing embed picks it up
// (web.go embeds all:static). Dev server proxies API/WS/SSE to the Go backend so
// the single-binary contract and the backend API surface stay unchanged.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { "@": path.resolve(__dirname, "src") },
  },
  build: {
    outDir: "static",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api/ws": { target: "ws://localhost:8080", ws: true },
      "/api": { target: "http://localhost:8080", changeOrigin: true },
    },
  },
});
