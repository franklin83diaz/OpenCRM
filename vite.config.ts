import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Where the UI's /api calls go: wrangler dev (the Worker and the API's
// container) on 8787, or API_URL, such as the Go dev server's
// http://localhost:8790 (README: a faster loop for Go changes).
const api = process.env.API_URL || "http://localhost:8787";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src/client"),
    },
  },
  server: {
    // The address the README sends people to.
    port: 5175,
    proxy: {
      "/api": {
        target: api,
        changeOrigin: true,
      },
      // The API's description for agents, linked from Settings → API tokens.
      "/llms.txt": {
        target: api,
        changeOrigin: true,
      },
    },
  },
});
