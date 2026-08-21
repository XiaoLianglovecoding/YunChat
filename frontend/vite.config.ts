import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "0.0.0.0",
    port: 5173,
    proxy: {
      "/api": "http://localhost:18080",
      "/healthz": "http://localhost:18080",
      "/ws": { target: "ws://localhost:18080", ws: true },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
