# 架构总览

## 进程与入口

```
cmd/server/main.go
  ├─ config.InitApp()       读取 config.json / account.json / tasks.json / settings.json
  ├─ server.Run(cfg)        go-zero HTTP server + 路由装配
  └─ 托盘 / 信号处理        Windows 托盘；Linux/Docker 用 --no-tray
```

核心模块：

| 路径 | 职责 |
|---|---|
| `internal/server` | 路由、wire、中间件、启动装配 |
| `internal/handler` | HTTP 接口：STRM、Emby、设置、清理、任务、通知 |
| `internal/service/task` | 全量/增量扫描、写入 STRM、补建、任务运行时 |
| `internal/service/monitor` | 115 生活事件：create/move/rename/delete、字幕同步 |
| `internal/service/strm` | STRM 内容生成、路由决策、直链缓存、Managed 登记 |
| `internal/service/client115` | 115 API、目录/事件/下载直链、Cookie 判定 |
| `internal/service/embyproxy` | Emby 反向代理、PlaybackInfo 改写、媒体流代理 |
| `internal/service/db` | SQLite：files/folders/managed_strms/history/trash |
| `internal/service/store` | 账号、设置、任务的持久化存储 |
| `internal/service/notify` | Telegram 通知、轮询、命令处理 |
| `pkg/strmutil` | STRM 文件名、原子写入、路径安全、回收站 |

## 数据目录

- 配置：`config.json`、`account.json`、`tasks.json`、`settings.json`
- 数据：`data/filePathDb.sqlite`（WAL）
- 日志：由 `pkg/logger` 初始化，具体路径见启动日志
- STRM 输出目录：由任务/生活事件的本地映射决定

## 一次播放的路径

1. 播放器读取 `.strm`，拿到 `http://<prefix>/api/strm?account=..&pickcode=..`。
2. `/api/strm` 取 115 直链，按 UA/格式/并发决定 302 或服务端代理。
3. 经 Emby 播放时，客户端连接 Emby 反代端口；反代改写 PlaybackInfo 为 DirectPlay，再处理媒体流。
4. 原盘格式在反代层和 STRM 端点层都走代理，Range 分片从 CDN 拉取。

详见 [播放链路](playback-pipeline.md)。

## 版本与构建

- `cmd/server/main.go` 的 `version` 与 `frontend/package.json` 必须一致。
- 前端构建产物写入 `internal/web/spa`，由 `go:embed` 嵌入。
- 发布由 tag 触发 GitHub Actions，具体见 `docs/dev/release.md`。
