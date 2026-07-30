import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 3001,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        ws: true,
      },
      // WeKnora UI proxy (for iframe embedding in admin panel — same origin)
      "/weknora-ui": {
        target: "http://localhost:8082",
        changeOrigin: true,
        ws: true,
        rewrite: (path) => path.replace(/^\/weknora-ui/, ""),
      },
    },
  },
});
