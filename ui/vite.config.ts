import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The daemon serves the built SPA from ui/dist (embedded via go:embed) at /.
// In dev, `npm run dev` runs Vite on :5173 and proxies /api + /mcp to the
// running daemon so the browser talks to real data without CORS.
const DAEMON = process.env.VOR_DAEMON ?? "http://127.0.0.1:7337";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": { target: DAEMON, changeOrigin: true },
      "/mcp": { target: DAEMON, changeOrigin: true },
    },
  },
});
