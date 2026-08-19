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
    // 开发模式下 Vite dev server 用 5173（Vite 官方默认），避免与 Go 后端生产端口 8090 冲突。
    // 两个进程可同时启动互不影响：浏览器访问 5173 拿页面 (HMR 热更新)，/api 代理到 Go 8090。
    // 生产模式：npm run build 产出 internal/web/spa，由 Go embed 与 API 共用单端口 8090 同源提供。
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8090",
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "../internal/web/spa",
    emptyOutDir: true,
    sourcemap: false,
  },
});
