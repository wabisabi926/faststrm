# FastStrm Frontend（Vite + React）

纯 Vite + React + TypeScript 构建的 FastStrm Web UI，生产时由 Go 后端通过 `go:embed` 嵌入到单二进制里，与 API 同源共用 `8090` 端口提供服务；开发模式下 Vite 独立启动做 HMR 热更新。

## 开发模式

```bash
npm install
npm run dev
```

默认监听 <http://localhost:5173>，`/api/*` 请求会自动反向代理到 Go 后端 <http://localhost:8090>。

## 构建并嵌入 Go

```bash
npm run build
# 产物输出到 ../internal/web/spa/（vite.config.ts outDir）
# Go 构建时通过 //go:embed 嵌入该目录
```

端口约定：
- **开发模式**：浏览器访问 `5173`（Vite HMR）+ API 代理到 `8090`（Go）
- **生产模式**：单二进制，只访问 `8090`（页面和 API 同源）
