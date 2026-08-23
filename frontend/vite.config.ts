import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";
import pkg from "./package.json";

export default defineConfig({
  plugins: [react()],
  base: "",
  define: {
    __APP_VERSION__: JSON.stringify(pkg.version),
    __APP_NAME__: JSON.stringify(pkg.name),
  },
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
          if (id.includes("@radix-ui")) return "radix-ui";
          if (id.includes("lucide-react")) return "icons";
          if (/node_modules\/(react|react-dom|react-router|scheduler|zustand|use-sync-external-store)\//.test(id)) return "react-core";
          // markdown 相关库（react-markdown/remark/rehype/micromark/unified 等）不分独立 chunk
          // 之前分 markdown chunk 会导致 Circular chunk: markdown -> vendor -> markdown
          // 引发 "Cannot access 'kn' before initialization" 运行时错误
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
