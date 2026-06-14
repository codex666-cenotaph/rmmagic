import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // ws: true so the remote-shell WebSocket upgrade is proxied too.
      "/api": { target: "http://localhost:8080", ws: true },
      "/agent": { target: "http://localhost:8080", ws: true },
    },
  },
});
