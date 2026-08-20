import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  base: "",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (id.includes("@assistant-ui") || id.includes("assistant-stream") || id.includes("assistant-cloud")) return "assistant-ui";
          if (/react-markdown|remark-|rehype-|micromark|mdast-|hast-|unified/.test(id)) return "markdown";
          if (id.includes("@radix-ui")) return "radix-ui";
          if (id.includes("lucide-react")) return "icons";
          if (/node_modules\/(react|react-dom|react-router|scheduler|zustand|use-sync-external-store)\//.test(id)) return "react-core";
          return "vendor";
        },
      },
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
    },
  },
});
