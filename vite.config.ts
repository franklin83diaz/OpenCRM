import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

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
    // The address the README sends people to; the API is wrangler on 8787.
    port: 5175,
    proxy: {
      "/api": {
        target: "http://localhost:8787",
        changeOrigin: true,
      },
      // The API's description for agents, linked from Settings → API tokens.
      "/llms.txt": {
        target: "http://localhost:8787",
        changeOrigin: true,
      },
    },
  },
});
