# FastStrm Go 语言重构实施计划

> 版本：v1.0（优化版）
> 基础版本约束：Go 1.21+
> 迁移策略：保留 nginx 8091 端口（策略A），降低迁移风险
> 前端策略：Next.js 前端暂不迁移，Go 后端完全兼容现有前端 API

---

## 一、重构目标与原则

### 1.1 核心目标
- **运行时性能提升**：利用 Go 原生并发模型，消除 Node.js 单线程瓶颈
- **内存占用优化**：降低长期运行的 RSS 内存占用
- **部署简化**：单一二进制文件，消除 Node.js 运行时依赖
- **跨平台兼容**：支持 Linux x86_64 / ARM64、Windows、macOS

### 1.2 迁移原则
| 原则 | 说明 |
|------|------|
| **API 零中断** | Go 后端 100% 兼容现有 Next.js 前端的所有 API 接口 |
| **数据兼容** | SQLite 表结构、Bolt KV 缓存、JSON 配置文件与现有格式完全一致 |
| **渐进迁移** | 分阶段交付，每阶段均可独立验证 |
| **可回滚** | 保留原 Node.js 实现，出现问题可快速切回 |
| **精确移植** | 加密算法、SSE 协议、限流器等核心逻辑严格对齐 TypeScript 实现 |

---

## 二、技术选型（优化后）

| 组件 | 选型 | 版本约束 | 选型理由 |
|------|------|----------|----------|
| Web 框架 | **go-zero** | v1.6+ | 统一的工程化规范、中间件生态、SSE 支持 |
| 数据库 | **SQLite (modernc.org/sqlite)** | 纯 Go 实现 | 无需 CGO，跨平台编译；表结构复用 `filePathDb.ts` |
| KV 缓存 | **bbolt (go.etcd.io/bbolt)** | v1.3+ | 与现有 Bolt 缓存格式兼容 |
| 日志 | **zap** | v1.26+ | 结构化日志，高性能 |
| 定时任务 | **robfig/cron/v3** | v3.0+ | 兼容 Cron 表达式，支持任务调度 |
| HTTP 客户端 | **go-zero/resty** 或标准库 | - | 连接池复用，超时控制 |
| 配置管理 | **cleanenv + JSON 文件** | - | 与现有 `settings.json` 格式兼容 |
| 限流 | **golang.org/x/time/rate** + 自定义 Bottleneck | - | 令牌桶 + 并发数限制，对齐 TS 实现 |
| SSE | **go-zero 原生 + eventsource.Server** | - | 严格遵守 `data: {JSON}\n` 帧格式 |
| JWT | **golang.org/x/crypto** | - | 对齐现有 JWT 签发/校验逻辑 |
| 加密 | **crypto/aes + crypto/rsa + crypto/sha256** | - | 标准库实现，无第三方依赖风险 |

---

## 三、项目目录结构

```
faststrm-go/
├── cmd/
│   └── server/
│       └── main.go                  # 入口：初始化 + 启动服务
├── internal/
│   ├── config/
│   │   ├── config.go                # 配置结构体 + settings.json 加载
│   │   └── init.go                  # ⭐ initApp：迁移 docker-entrypoint.sh 初始化逻辑
│   ├── server/
│   │   ├── server.go                # go-zero server 启动、路由注册
│   │   ├── router.go                # API 路由定义（对齐 frontend/src/app/api/*）
│   │   └── middleware/
│   │       ├── auth.go              # JWT 鉴权中间件（对齐 middleware.ts）
│   │       ├── cors.go              # CORS 处理
│   │       └── sse.go               # SSE 响应头设置
│   ├── handler/                      # API Handler 层（对应 route.ts）
│   │   ├── auth.go                  # /api/auth/* 登录/改密
│   │   ├── account.go               # /api/account/* 账号管理
│   │   ├── task.go                  # /api/startTask /api/cancelTask /api/task
│   │   ├── task_history.go          # /api/taskHistory /api/taskLog/:taskId
│   │   ├── directory.go             # /api/directory/* 目录树
│   │   ├── strm.go                  # /api/strm 路由引擎（decideRoute）
│   │   ├── strm_cleanup.go          # /api/strmCleanup/*
│   │   ├── fs.go                    # /api/fs/*
│   │   ├── settings.go              # /api/settings
│   │   ├── emby.go                  # /api/emby/* + webhook
│   │   ├── notify.go                # /api/notify/* Telegram/Webhook
│   │   ├── life_monitor.go          # /api/lifeMonitor /api/lifeEvents
│   │   ├── media_mount_sync.go      # /api/mediaMountSync
│   │   ├── health.go                # /api/health
│   │   └── clear_directory.go       # /api/clearDirectory
│   ├── service/                      # 业务逻辑层（对应 lib/*.ts）
│   │   ├── crypto115/               # ⭐ 115crypto 精确移植
│   │   │   ├── crypto.go            # RSA+XOR encrypt/decrypt（G_kts 硬编码）
│   │   │   └── keys.go              # 密钥派生逻辑
│   │   ├── pwdcrypto/               # ⭐ 凭据加密
│   │   │   ├── aes_gcm.go           # AES-256-GCM encryptCredentials
│   │   │   └── hash.go              # SHA-256 hashPassword / verifyPassword
│   │   ├── client115/               # 115 API 客户端
│   │   │   ├── qrcode.go            # 扫码登录流程
│   │   │   ├── files.go             # fs_files / fs_dir_getid / exportDirParse
│   │   │   ├── life.go              # 115Life 生活事件接口
│   │   │   ├── client.go            # HTTP 封装 + 限流 + cookie jar
│   │   │   └── types.go             # 115 API 返回结构体
│   │   ├── task/                     # 任务执行引擎
│   │   │   ├── executor.go          # executeTask 主流程
│   │   │   ├── download.go          # startDownloadTask + 并发下载
│   │   │   ├── scheduler.go         # Cron 调度（taskScheduler.ts）
│   │   │   └── state.go             # 任务状态机 + SSE 推送
│   │   ├── strm/                     # STRM 模块
│   │   │   ├── router.go            # ⭐ decideRoute 策略引擎
│   │   │   ├── execute.go           # runExecute：清理 + 生成
│   │   │   ├── scan.go              # STRM 扫描
│   │   │   ├── cleanup.go           # 清理对账
│   │   │   └── fileops.go           # STRM 文件读写
│   │   ├── monitor/                  # 生活事件监控
│   │   │   ├── monitor.go           # startMonitor + oncePoll
│   │   │   ├── event_handler.go     # processEvent 文件创建/删除/重命名
│   │   │   └── state.go             # 监控状态 + 挂起/恢复
│   │   ├── emby/                     # Emby 集成
│   │   │   ├── client.go            # Emby API 客户端
│   │   │   ├── notifier.go          # 通知分发
│   │   │   ├── templates.go         # 通知模板
│   │   │   └── syncdel.go           # 删除同步
│   │   ├── notify/                   # 通知模块
│   │   │   ├── telegram.go          # Telegram Bot + Polling + 命令
│   │   │   ├── webhook.go           # Webhook 通知
│   │   │   └── dispatcher.go        # 分发器
│   │   ├── db/                       # 数据访问层
│   │   │   ├── sqlite.go            # SQLite 连接 + PRAGMA 设置
│   │   │   ├── filepath_repo.go     # ⭐ filePathDb 表操作 + 批量 upsert
│   │   │   ├── task_history_repo.go # 任务历史
│   │   │   └── life_event_repo.go   # 生活事件日志
│   │   ├── cache/                    # Bolt KV 缓存
│   │   │   └── bolt.go              # SimpleCache 等价实现
│   │   ├── runtime/                  # 运行时状态
│   │   │   └── account_state.go     # ⭐ accountRuntimeState 扫描锁/心跳
│   │   ├── rate/                     # ⭐ 限流体系（统一管理）
│   │   │   ├── limiter.go           # 账号级限流（令牌桶）
│   │   │   ├── bottleneck.go        # 并发数限制（Bottleneck）
│   │   │   └── registry.go          # 按账号+类型注册限流器
│   │   ├── auth/                     # 鉴权
│   │   │   └── jwt.go               # JWT 签发/校验
│   │   └── sse/                      # ⭐ SSE 服务
│   │       ├── server.go            # EventSource 服务器
│   │       └── events.go            # 事件定义 + JSON 序列化
│   └── model/                        # 数据模型
│       ├── account.go
│       ├── task.go
│       ├── strm.go
│       ├── emby.go
│       ├── settings.go              # settings.json 结构体（对齐配置项参考.md）
│       └── api.go                   # API 请求/响应结构体
├── pkg/                             # 可复用公共包
│   ├── utils/                       # 工具函数（对齐 utils.ts）
│   ├── logger/                      # zap 封装
│   └── retry/                       # 重试逻辑
├── data/                            # 运行时数据目录（挂载卷）
│   ├── db/                          # SQLite 文件
│   ├── cache/                       # Bolt KV 文件
│   └── config/                      # settings.json
├── Dockerfile.go                    # Go 多阶段构建 Dockerfile
├── docker-compose.go.yml            # Go 版本编排文件
└── go.mod
```

---

## 四、实施计划（7 阶段）

### ═══════════════════════════════════════
### 阶段 1：基础脚手架与初始化逻辑
### ═══════════════════════════════════════

**目标**：搭建 Go 项目骨架，完成配置加载和初始化逻辑迁移，服务可健康启动。

**关键任务**：

| # | 任务 | 对应源文件 | 输出/验证 |
|---|------|-----------|-----------|
| 1.1 | 初始化 Go Module，安装核心依赖 | - | `go.mod` + 依赖锁定 |
| 1.2 | 定义 `model/settings.go` 配置结构体 | `docs/配置项参考.md` | 覆盖 download/strm/emby/telegram/lifeMonitor 全部字段 |
| 1.3 | 实现 `config/config.go` 加载 `settings.json` | `docker-entrypoint.sh` | 支持环境变量覆盖 + JSON 默认值 |
| 1.4 | ⭐ **实现 `config/init.go` (initApp)** | `docker-entrypoint.sh` | ① 拷贝默认配置到 /data/config<br>② 首次启动生成 admin 密码 SHA-256 哈希<br>③ 生成 JWT_SECRET 随机令牌<br>④ 确保 data/db、data/cache 目录存在 |
| 1.5 | 实现 `pkg/logger` zap 封装 | `lib/logger.ts` | 分级日志 + 文件滚动 |
| 1.6 | 实现 go-zero server 启动骨架 | - | HTTP 监听端口可配置 |
| 1.7 | 实现 `/api/health` 健康检查接口 | `api/health/route.ts` | 返回 `{status:"ok",version:"..."}` |
| 1.8 | 编写 `Dockerfile.go` 多阶段构建 | - | 镜像体积 < 50MB（基于 alpine） |
| 1.9 | 编写 `docker-compose.go.yml` | `docker-compose.yml` | 卷映射、端口映射与原编排一致 |

**验证标准**：
- `go run cmd/server/main.go` 启动无报错
- `curl /api/health` 返回 OK
- Docker 镜像可构建并正常启动
- 首次启动自动生成默认配置文件和密码哈希

---

### ═══════════════════════════════════════
### 阶段 2：数据层与加密模块（高风险，优先验证）
### ═══════════════════════════════════════

**目标**：完成加密算法精确移植和 SQLite/Bolt 数据层实现，通过单元测试确保字节级一致。

**关键任务**：

| # | 任务 | 对应源文件 | 输出/验证 |
|---|------|-----------|-----------|
| 2.1 | ⭐ **实现 `service/pwdcrypto`** | `lib/passwordCrypto.ts` | ① AES-256-GCM `encryptCredentials`<br>② SHA-256 `hashPassword`（加 salt）<br>③ `verifyPassword` 对比 |
| 2.2 | ⭐ **实现 `service/crypto115`（核心难点）** | `lib/115crypto.ts` | ① 硬编码 `G_kts` 密钥表<br>② 自定义 RSA+XOR `encrypt()`<br>③ 自定义 RSA+XOR `decrypt()`<br>④ 密钥派生函数 |
| 2.3 | 单元测试：pwdcrypto 与 TS 输出交叉验证 | - | 相同输入 → Go/TS 输出完全一致 |
| 2.4 | 单元测试：crypto115 与 TS 输出交叉验证 | - | 相同 plaintext + key → Go/TS ciphertext 一致 |
| 2.5 | 实现 `db/sqlite.go` 连接池 | `lib/filePathDb.ts` | PRAGMA 设置对齐：`journal_mode=WAL`、`synchronous=NORMAL`、`busy_timeout=5000` |
| 2.6 | ⭐ **实现 `db/filepath_repo.go`** | `lib/filePathDb.ts` | ① 建表 DDL 与 TS 完全一致<br>② `upsertFilePathEntryBatch` 批量 upsert<br>③ `removeGhostRecords` 清理幽灵记录 |
| 2.7 | 实现 `cache/bolt.go` | `lib/SimpleCache.ts` | Bolt KV 打开 + Get/Put/Delete |
| 2.8 | 实现 `service/auth/jwt.go` | `lib/jwt.ts` `lib/jwtSecret.ts` | JWT 签发 + 校验，与前端 middleware.ts 兼容 |

**验证标准**：
- crypto115/pwdcrypto 单元测试覆盖率 ≥ 90%
- 与 TypeScript 版本加密结果逐字节对比一致
- SQLite 可正确读写现有 TS 生成的 .db 文件（无 schema 变更）
- Bolt KV 可正确读写现有 TS 生成的缓存文件

---

### ═══════════════════════════════════════
### 阶段 3：账号体系与鉴权 API
### ═══════════════════════════════════════

**目标**：完成登录、账号管理（扫码登录）、鉴权中间件，前端可登录并浏览账号列表。

**关键任务**：

| # | 任务 | 对应源文件 | 输出/验证 |
|---|------|-----------|-----------|
| 3.1 | 实现 `middleware/auth.go` JWT 鉴权 | `frontend/src/middleware.ts` | 路由分组：公开路由（/login, /health, /qrcode/*）vs 受保护路由 |
| 3.2 | 实现 `handler/auth.go` | `api/auth/*/route.ts` | ① POST `/api/auth/login` 密码校验 + JWT<br>② POST `/api/auth/change-password` 改密码<br>③ POST `/api/auth/change-credentials` 改凭据加密密钥<br>④ POST `/api/auth/logout` |
| 3.3 | 实现 `service/client115/qrcode.go` | `lib/115.ts` getQrcodeToken/getQrcodeStatus/getQrcodeResult | 扫码登录三阶段 API 封装 |
| 3.4 | 实现 `handler/account.go` | `api/account/*/route.ts` | ① GET `/api/account` 账号列表<br>② POST `/api/account` 新增账号（cookie持久化）<br>③ GET/POST `/api/account/qrcode/token` `/status` `/cookie`<br>④ GET `/api/account/status` 在线状态 |
| 3.5 | 账号 cookie 持久化（Bolt 或 SQLite） | - | 使用 AES-256-GCM 加密存储 |
| 3.6 | ⭐ **实现 `service/runtime/account_state.go`** | `lib/accountRuntimeState.ts` | ① `tryEnterFullScan` 全量扫描锁<br>② `touchFullScanHeartbeat` 心跳更新<br>③ 监控挂起/恢复标志 |

**验证标准**：
- 前端可正常登录（密码校验通过）
- 扫码流程完整：获取二维码 → 手机扫码 → 获取 cookie → 保存账号
- 受保护路由无 JWT 返回 401
- 密码修改后旧密码失效

---

### ═══════════════════════════════════════
### 阶段 4：STRM 路由引擎（核心流量路径）
### ═══════════════════════════════════════

**目标**：实现 `/api/strm` 路由决策引擎，决定代理或重定向，完成 Emby 播放核心路径。

**关键任务**：

| # | 任务 | 对应源文件 | 输出/验证 |
|---|------|-----------|-----------|
| 4.1 | ⭐ **实现 `service/strm/router.go` (decideRoute)** | `app/api/strm/route.ts` | 决策逻辑对齐：<br>① UA 检测（Emby/Jellyfin/Infuse）<br>② 显式 mode 参数（proxy/redirect）<br>③ 账号状态检测<br>④ 返回决策：{action, url, headers} |
| 4.2 | 实现 `handleProxy`：流式代理 115 下载响应 | `app/api/strm/route.ts` | ① 透传 Range 请求头<br>② 流式转发（不缓存大文件）<br>③ 透传 Content-Type、Content-Length |
| 4.3 | 实现 `doRedirect`：302 重定向到 115 直链 | `app/api/strm/route.ts` | 带签名参数拼接 |
| 4.4 | 实现 `handler/strm.go` 注册路由 | - | GET `/api/strm?id=...` |
| 4.5 | ⭐ **实现 `service/rate` 限流体系** | `lib/rateLimiter.ts` | ① `Limiter`：令牌桶（QPS 限制）<br>② `Bottleneck`：并发数限制<br>③ `Registry`：按 `{accountId}:{type}` 注册<br>④ 下载专用限流与 API 调用限流分离 |
| 4.6 | 限流集成到 client115 和 strm proxy | - | 所有 115 API 调用和文件下载通过限流器 |
| 4.7 | 实现 `handler/fs.go` | `api/fs/get/route.ts` | 小型文件下载接口（非 STRM） |

**验证标准**：
- Emby UA → 默认走 redirect，显式 ?mode=proxy → 走 proxy
- 非 Emby UA → 默认走 proxy
- 限流生效：并发超过 Bottleneck 上限排队，QPS 超过令牌桶阻塞
- 大文件流式代理内存占用不增长（RSS 稳定）
- Range 请求支持正确：206 Partial Content

---

### ═══════════════════════════════════════
### 阶段 5：任务执行引擎 + SSE 实时推送
### ═══════════════════════════════════════

**目标**：实现同步任务的创建、执行（目录树构建→差异→并发下载→STRM生成），SSE 实时推送进度。

**关键任务**：

| # | 任务 | 对应源文件 | 输出/验证 |
|---|------|-----------|-----------|
| 5.1 | ⭐ **实现 `service/sse/server.go`** | - | ① 严格帧格式：`data: {JSON}\n\n`<br>② 支持多客户端订阅<br>③ `overallPercent` **序列化为字符串**（对齐前端期望） |
| 5.2 | SSE 事件类型定义 | `lib/taskExecutor.ts` | `progress` / `log` / `complete` / `error` 事件 |
| 5.3 | 实现 `service/client115/files.go` | `lib/115.ts` | ① `fs_files` 列目录<br>② `fs_dir_getid` 路径→ID<br>③ `exportDirParse` 目录全量导出 |
| 5.4 | 实现 `handler/directory.go` | `api/directory/*/route.ts` | GET `/api/directory/remote/list` 远程目录树<br>GET `/api/directory/local/list` 本地目录树 |
| 5.5 | 实现 `service/task/executor.go` | `lib/taskExecutor.ts` `executeTask` | 主流程：<br>① 构建远程目录树<br>② 构建本地 STRM 清单<br>③ 计算差异（新增/删除/保留）<br>④ 调用下载子流程 |
| 5.6 | 实现 `service/task/download.go` | `lib/taskExecutor.ts` `startDownloadTask` + `lib/downloadTaskManager.ts` | ① 并发下载（goroutine pool）<br>② 限流集成<br>③ STRM 文件写入<br>④ 进度上报到 SSE |
| 5.7 | 实现 `handler/task.go` | `api/startTask/route.ts` `api/cancelTask/route.ts` `api/task/route.ts` | ① POST `/api/startTask`<br>② ⭐ POST `/api/cancelTask` **同时支持 `id` 和 `taskId` 参数**<br>③ GET `/api/task` 运行中任务列表 |
| 5.8 | 实现 `handler/task_history.go` + DB repo | `api/taskHistory/route.ts` `api/taskLog/:taskId/route.ts` | 任务历史持久化 + 日志查询 |
| 5.9 | 实现 `handler/clear_directory.go` | `api/clearDirectory/route.ts` | 清空 STRM 输出目录（安全校验） |
| 5.10 | 实现 `service/task/scheduler.go` | `lib/taskScheduler.ts` | Cron 定时触发任务（robfig/cron/v3） |

**验证标准**：
- 创建任务 → SSE 推送 `progress` 事件，`overallPercent` 为字符串
- 取消任务：`id=xxx` 和 `taskId=xxx` 两种参数均生效
- 多任务并发执行不相互干扰
- 任务完成后历史可查，日志可浏览
- 定时任务按 Cron 表达式触发
- 异常场景（断网、账号下线）：任务状态变为 failed，错误信息写入日志

---

### ═══════════════════════════════════════
### 阶段 6：STRM 清理对账 + 生活事件监控 + Emby 集成 + 通知
### ═══════════════════════════════════════

**目标**：完成剩余业务模块，覆盖全部功能。

**关键任务**：

| 模块 | # | 任务 | 对应源文件 |
|------|---|------|-----------|
| **STRM 清理对账** | 6.1 | 实现 `service/strm/scan.go` | `lib/strmScan.ts` |
| | 6.2 | 实现 `service/strm/cleanup.go` + `execute.go` | `lib/strmCleanup.ts` `lib/strmExecute.ts` |
| | 6.3 | 实现 `handler/strm_cleanup.go` | `api/strmCleanup/*/route.ts` |
| **生活事件监控** | 6.4 | 实现 `service/client115/life.go` | `lib/115Life.ts` |
| | 6.5 | 实现 `service/monitor/monitor.go`（oncePoll 轮询） | `lib/eventMonitor.ts` |
| | 6.6 | 实现 `service/monitor/event_handler.go`（processEvent） | `lib/eventMonitorHandlers.ts` |
| | 6.7 | 实现 `handler/life_monitor.go` + life_event_repo | `api/lifeMonitor/route.ts` `api/lifeEvents/route.ts` |
| | 6.8 | 监控挂起/恢复与任务扫描锁联动 | `lib/eventMonitorState.ts` |
| **Emby 集成** | 6.9 | 实现 `service/emby/client.go` | `lib/emby/client.ts` |
| | 6.10 | 实现 `service/emby/notifier.go` + dispatcher | `lib/emby/notifier*.ts` |
| | 6.11 | 实现 `service/emby/syncdel.go` | `lib/emby/syncDel.ts` + `lib/syncDelHistory.ts` |
| | 6.12 | 实现 `handler/emby.go` | `api/emby/*/route.ts` |
| | 6.13 | POST `/api/emby/webhook` 验证连通性 | - |
| | 6.14 | 实现 `handler/media_mount_sync.go` | `api/mediaMountSync/route.ts` + `lib/mediaMountSync.ts` |
| **通知模块** | 6.15 | 实现 `service/notify/telegram.go` | `lib/telegram.ts` `lib/telegramPolling.ts` `lib/telegramCommands.ts` |
| | 6.16 | 实现 `service/notify/webhook.go` | `lib/notifierSender.ts` Webhook 部分 |
| | 6.17 | 实现 `service/notify/dispatcher.go` | `lib/emby/notifierDispatcher.ts` 通用化 |
| | 6.18 | 实现 `handler/notify.go` | `api/notify/*/route.ts` |
| **设置接口** | 6.19 | 实现 `handler/settings.go` | `api/settings/route.ts` |
| | 6.20 | 热更新：部分配置修改后无需重启生效 | - |

**验证标准**：
- STRM 清理对账：正确识别幽灵文件、缺失文件，报告与 TS 版本一致
- 生活事件监控：轮询 115 事件 → 自动触发增量任务 → 自动 STRM 生成
- Emby Webhook 收播事件 → Telegram 消息送达
- Emby 删除事件 → 同步删除本地 STRM
- Telegram Bot 命令响应正常

---

### ═══════════════════════════════════════
### 阶段 7：集成测试、性能优化、上线切换
### ═══════════════════════════════════════

**目标**：全链路 E2E 测试，性能基线对比，灰度上线。

**关键任务**：

| # | 任务 | 验证标准 |
|---|------|----------|
| 7.1 | 回归测试：全 API 接口对比 Go vs Node 响应 | JSON 结构一致，字段类型一致（注意 overallPercent 等字符串字段） |
| 7.2 | E2E：完整用户流程（登录→加账号→建任务→STRM生成→Emby播放→收到通知） | 全流程无报错，体验一致 |
| 7.3 | ⭐ **压力测试：STRM 路由代理** | 100 并发 Emby 播放请求，内存稳定，延迟 p99 < 500ms |
| 7.4 | 压力测试：任务引擎 10 任务并发 | CPU 利用率、内存占用优于 Node.js 版 |
| 7.5 | 长时间稳定性测试（72h soak test） | 无内存泄漏，RSS 不持续增长，定时任务按时触发 |
| 7.6 | ⭐ **上线切换策略（nginx 策略A）** | ① 现有 nginx 8091 端口保留不变<br>② Go 后端监听 8090 端口<br>③ nginx `upstream` 中新增 `server 127.0.0.1:8090 backup`<br>④ 灰度：先分流 10% `/api/strm` 到 Go 后端<br>⑤ 观察 24h → 逐步调大比例 → 最终 100% 切 Go |
| 7.7 | 回滚预案 | Go 异常 → nginx upstream 权重改回 Node → 立即恢复 |
| 7.8 | 文档：部署说明、运维手册 | 与现有 docs 对齐 |

---

## 五、风险与缓解措施

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| ⚠️ crypto115 算法移植错误（RSA+XOR 自定义） | 🔴 高 | 阶段 2 强制与 TS 版逐字节对比单元测试；硬编码 G_kts 直接复制 |
| ⚠️ SQLite 并发锁问题（WAL 模式） | 🟡 中 | PRAGMA busy_timeout=5000；写操作统一走 goroutine 串行队列 |
| ⚠️ SSE 协议格式不兼容导致前端不接收 | 🟡 中 | 阶段 5 明确：`data:` 前缀 + `\n\n` 分隔 + `overallPercent` 字符串化 |
| ⚠️ 限流粒度不当导致 115 封号 | 🔴 高 | 复用 TS 版调优后的限流参数；账号级+类型级分离，下载单独限流 |
| ⚠️ 任务取消不彻底导致 goroutine 泄漏 | 🟡 中 | 所有阻塞操作使用 `context.Context`；父 ctx 取消 → 子任务全部退出 |
| ⚠️ nginx 切换期间少量请求失败 | 🟡 中 | 灰度切换 + upstream 健康检查；观察期不低于 24h |

---

## 六、里程碑与交付物

| 里程碑 | 对应阶段 | 预计耗时 | 交付物 |
|--------|---------|----------|--------|
| M1 项目骨架可运行 | 阶段 1 | 2-3 天 | 可启动服务 + Dockerfile |
| M2 加密与数据层通过测试 | 阶段 2 | 3-5 天 | crypto115/pwdcrypto 单元测试报告 + DB 集成 |
| M3 账号体系打通 | 阶段 3 | 2-3 天 | 前端可登录 + 扫码登录流程跑通 |
| M4 Emby 播放核心路径可用 | 阶段 4 | 3-4 天 | STRM 路由决策 + 代理 + 限流 |
| M5 任务引擎跑通 + 实时进度 | 阶段 5 | 4-6 天 | 任务创建/执行/取消/调度 + SSE 推送 |
| M6 功能全覆盖 | 阶段 6 | 5-7 天 | 清理对账/生活监控/Emby/通知全部模块 |
| M7 性能达标 & 灰度上线 | 阶段 7 | 3-5 天 | 性能报告 + nginx 灰度切流完成 |

**合计预计工期：22-33 个工作日（约 4.5-7 周）**

---

## 七、开发环境配置速查

```bash
# 1. Go 版本
go version  # 要求 >= 1.21

# 2. 初始化
cd faststrm-go
go mod init github.com/wabisabi926/faststrm
go get -u github.com/zeromicro/go-zero@latest
go get -u modernc.org/sqlite
go get -u go.etcd.io/bbolt
go get -u go.uber.org/zap
go get -u github.com/robfig/cron/v3
go get -u golang.org/x/time

# 3. 运行
go run cmd/server/main.go
# 监听默认端口：8090（nginx 上游）

# 4. 单元测试
go test ./internal/service/crypto115/... -v -count=1
go test ./internal/service/pwdcrypto/... -v -count=1

# 5. Docker 构建
docker build -f Dockerfile.go -t faststrm-go:latest .
```

---

> **文档状态**：待执行  
> **下次审核点**：阶段 1 完成后，评估 go-zero 框架是否适配，如有严重问题可回退到 Gin + 自行封装中间件方案
