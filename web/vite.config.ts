import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// MOLTNET_DEV_API points the dev console at another node, e.g. a public
// read-only mirror, so the UI can be developed against real traffic.
const env = (mode: string) => loadEnv(mode, ".", "MOLTNET_");

export default defineConfig(({ mode }) => ({
  // Console is served from /console/ both in production and in dev so the
  // SPA's URL routes (/console/room/:id, /console/dm/:id, /console/events)
  // resolve relative asset paths consistently.
  base: "/console/",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    target: "es2022",
  },
  server: {
    proxy: {
      "/v1": {
        target: env(mode).MOLTNET_DEV_API || "http://127.0.0.1:8787",
        changeOrigin: true,
        // MOLTNET_DEV_TOKEN signs the dev console in, e.g. as a local operator.
        headers: env(mode).MOLTNET_DEV_TOKEN
          ? { Authorization: `Bearer ${env(mode).MOLTNET_DEV_TOKEN}` }
          : undefined,
      },
    },
  },
}));
