import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";
import fs from "node:fs";

// ffmpegCore makes the @ffmpeg/core wasm available SAME-ORIGIN under /ffmpeg/*.
// The package's exports map only exposes "." and "./wasm" (no deep umd path), so
// neither a Vite `?url` import nor require.resolve can reach the umd build — we
// locate the files directly on disk under node_modules instead and (a) copy them
// into the build output (web/static → embedded in the Go binary) and (b) serve
// them from node_modules during dev. This removes the external CDN the loader
// used before, which was unreachable on the internal network and left 抽帧/裁剪
// permanently disabled.
function ffmpegCore(): Plugin {
  const umdDir = path.resolve(__dirname, "node_modules/@ffmpeg/core/dist/umd");
  const files: Record<string, string> = {
    "ffmpeg-core.js": path.join(umdDir, "ffmpeg-core.js"),
    "ffmpeg-core.wasm": path.join(umdDir, "ffmpeg-core.wasm"),
  };
  return {
    name: "ffmpeg-core-assets",
    // Dev: serve /ffmpeg/<name> straight from node_modules.
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const m = req.url && /^\/ffmpeg\/(ffmpeg-core\.(?:js|wasm))(?:\?|$)/.exec(req.url);
        if (!m) return next();
        const src = files[m[1]];
        if (!src || !fs.existsSync(src)) return next();
        res.setHeader("Content-Type", m[1].endsWith(".wasm") ? "application/wasm" : "text/javascript");
        fs.createReadStream(src).pipe(res);
      });
    },
    // Build: copy into the output dir after it's been written (emptyOutDir has
    // already run, so the copies survive).
    writeBundle(options) {
      const outDir = options.dir ?? path.resolve(__dirname, "static");
      const destDir = path.join(outDir, "ffmpeg");
      fs.mkdirSync(destDir, { recursive: true });
      for (const [name, src] of Object.entries(files)) {
        fs.copyFileSync(src, path.join(destDir, name));
      }
    },
  };
}

// Build output goes to web/static so the Go binary's existing embed picks it up
// (web.go embeds all:static). Dev server proxies API/WS/SSE to the Go backend so
// the single-binary contract and the backend API surface stay unchanged.
export default defineConfig({
  plugins: [react(), ffmpegCore()],
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
