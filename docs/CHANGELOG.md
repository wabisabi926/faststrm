# FastStrm 变更日志

## v0.9.6 (2026-08-21)

### 飞牛 fNOS 路径权限对齐 qmediasync

- **directory.go**: `defaultRoots` fNOS 分支改为仅返回 `TRIM_DATA_*` 白名单路径，不再枚举 `/proc/mounts` 系统挂载点；未授权时返回友好提示
- **LocalDirectoryTreeDialog.tsx**: 新增 `rootMessage` state，空列表时显示后端提示 + 路径跳转引导

### 删除错误的自动填写功能

- **DirectoryTreeDialog.tsx**: 删除远程路径选择后的「自动填写本地路径」弹窗；用户需通过本地路径选择器独立选择有效绝对路径
- **AddTaskDialog.tsx**: 清理 `onSelectWithTargetPath` 回调

### 修复任务日志查看

- **task/index.tsx**: 修复日志判断逻辑（后端返回 text/plain，前端之前用 JSON 字段判断导致完全失效）；未执行任务用 toast.info 提示而非报错

### 监控状态显示修复

- **monitor.go**: `handlePollError` 排除 `context.Canceled/DeadlineExceeded` 正常停止行为
- **settings.tsx**: 状态显示优先级改为：有错误(红色「异常」) > 运行中(绿色) > 待保存(黄色) > 已停止(灰色)

### 生活事件监控 API 修复

- **life.go**: LifeClient.doRequest 增加 HTTP 状态码检查，404/500 返回清晰错误；PullEvents 路径 `life.115.com/.../live/listhistory` → `web.api.115.com/.../life/listhistory` 且改为 GET；LifeShow 路径同步修正

### 版本号全链路同步 0.9.6

- `cmd/server/main.go`、`frontend/package.json`、`FNOS/faststrm-amd64/manifest`、`FNOS/faststrm-arm64/manifest`

## v0.9.5 (2026-08-20)

### 跨平台本地目录浏览大修（飞牛 fNOS / Docker / Linux / Windows）

针对 v0.9.4 在飞牛 fNOS 沙箱下"只能看到 @appshare 一个目录、无法选择媒体文件夹"的 P0 问题彻底修复。

- **directory.go**: 重写 `defaultRoots` 为 4 级优先根列表（`FASTSTRM_LOCAL_DIR_ROOTS` 显式覆盖 → Linux `/proc/mounts` 真实挂载点 → fNOS 白名单 ∪ NAS 常见卷根 → 硬编码兜底）；`isPathAllowed` fNOS 默认宽松，除非显式设置 `FASTSTRM_FNOS_STRICT_PATH=1`，否则一律放行只打 warning；新增 `readRealMountpoints()` 跨平台安全读取挂载点，过滤 proc/sysfs/devpts/cgroup/overlay/tmpfs 虚拟 FS
- **manifest (amd64/arm64)**: 新增 `share_dirs = /vol1,/volume1,/mnt/user,/mnt/ssd,/mnt/cache,/mnt/disk,/share,/public,/home` 字段，对齐 qmediasync 共享路径声明规范；用户在飞牛管理后台勾选共享文件夹后会真正 bind mount 进沙箱并写入 `TRIM_DATA_SHARE_PATHS`
- **LocalDirectoryTreeDialog.tsx**: 顶部新增手动路径输入框（等宽字体 + Enter 提交）+ 「跳转」按钮（带 loading / 禁用态）；三路校验状态反馈（✅ ok / ❌ err / 💡 idle）；选择树节点同步回填输入框；对话框关闭自动清理状态

### CI 升级

- 升级 GitHub Actions 到 Node 24 native：`actions/checkout@v5` / `setup-node@v5` / `setup-go@v6` / `upload-artifact@v5` / `download-artifact@v5`（共 17 处替换，消除 Node.js 20 弃用告警）

### 版本号全链路同步 0.9.5

- `cmd/server/main.go`、`frontend/package.json`、`FNOS/faststrm-amd64/manifest`、`FNOS/faststrm-arm64/manifest`

### 文档与 wiki 同步

- `Readme.md` 最新版本段落与下载链接同步到 v0.9.5
- `docs/CHANGELOG.md` 新增 v0.9.5 章节
- `wiki_drafts/Home.md` 版本公告更新为 v0.9.5
- `wiki_drafts/版本更新日志.md` 新增 v0.9.5 完整章节
- `.gitignore` 补充 `.config/*.json` 规则

## v0.9.4 (2026-08-20)

### 紧急修复：路径浏览 & 账户状态

- **directory.go**: 远程目录列表新增 `cid` 查询参数支持，优先使用 CID 直接导航，修复根目录路径处理和路径格式清理
- **directory.go**: 修复 `isDir` 判断逻辑（`fc > 0 && fid == 0`），对齐 115 web API 规范
- **files.go**: 优化 `FsDirGetID` 路径格式处理，确保路径以 `/` 开头
- **files.go**: 修复 `FsFiles` 中 `isDir` 判断逻辑
- **directory.go**: 优化 `defaultRoots()` 函数，增加 fNOS 环境变量（`TRIM_DATA_ACCESSIBLE_PATHS`、`TRIM_DATA_SHARE_PATHS`）探测和路径权限校验
- **directory.go**: Windows 环境使用 gopsutil/disk 枚举盘符或回退扫描
- **DirectoryTreeDialog.tsx**: 前端改为使用 CID 参数进行 API 调用，新增 `cid`、`fid`、`path` 字段
- **account-alerts.tsx**: 添加账户状态检查功能，调用 `/api/account/status` 接口，显示状态图标（正常/异常/未知），添加刷新按钮，自动检测状态

## v0.9.3 (2026-08-20)

### 紧急修复

- **FNOS cmd/main**: 添加 `DEFAULT_CONFIG_DIR` 环境变量指向 `.config` 模板目录，修复 fNOS 环境下应用无法启动的问题
- **directory.go**: 将本地目录列表接口从 GET 改为 POST，支持 JSON body 参数 `basePath`；修复响应中 `id` 字段使用完整路径而非索引
- **directory.go**: 优化 `defaultRoots()` 函数，优先返回 fNOS 环境变量目录和常见挂载点（/mnt、/media、/home 等）
- **directory.go**: 远程目录列表修复 `id` 唯一性问题，目录优先用 `cid`，文件用 `fid`，兜底用序号
- **task.go**: `HandleListTasks` 增加 nil 检查和容错处理，`ReadTasks` 失败时返回空列表而非 500 错误
- **media_mount.go**: `HandleMediaMountSyncGET/POST` 在 `ReadSettings` 或 `ReadTasks` 失败时降级到默认值/空列表，不再返回 500 错误
- **settings.go**: `ReadSettings` 权限不足或 JSON 解析失败时返回默认配置而非报错
- **directory.go**: 添加路径穿越防护（filepath.Clean、filepath.IsAbs、os.Stat）

## v0.9.2 (2026-08-20)

### Bug 修复

- **executor.go**: 修复 rt.Register(task) 返回的 cancel 函数被丢弃，导致资源泄漏；添加 defer cancel() 确保任务取消时资源被正确释放
- **executor.go**: 修复 SettingsStore.ReadSettings() 错误被忽略，读取失败时回退到默认设置
- **executor.go**: 修复 json.Marshal(v) 错误被忽略，序列化失败时返回空字符串
- **clear_directory.go**: 修复 io.ReadAll(r.Body) 错误被忽略，添加错误检查和返回
- **clear_directory.go**: 修复 json.Unmarshal 错误被忽略，添加错误检查和返回
- **clear_directory.go**: 修复 filepath.Walk 错误被忽略，回调中的错误记录日志
- **media_mount.go**: 修复 json.NewDecoder 解码错误被忽略，添加错误检查和返回
- **media_mount.go**: 删除无用的 model.DefaultSettings 引用
- **emby/client.go**: 修复 io.ReadAll 错误被忽略（4 处），在 GetItemDetail、FindItemByPath、RefreshItem、RefreshLibrary 中添加错误检查

### 安全加固

- **clear_directory.go**: 添加路径穿越防护，检查 .. 路径段、清理路径、转换为绝对路径，防止恶意路径访问攻击

### 代码优化

- **executor.go**: 提取魔法数字 99.995 为常量 taskCompleteThreshold，提高代码可维护性
- **executor.go**: 将 strmWorkers 硬编码值 20 提取为可配置项 strmMaxConcurrent，支持通过 settings.json 动态调整 STRM 写入并发数
- **settings.go**: 新增 StrmMaxConcurrent 配置字段（默认 20），允许用户根据系统资源调整并发度
- **settings.go**: 新增 AutoDownloadMetadata 配置字段（默认 true），允许用户控制是否自动下载 nfo/jpg/png 等元数据文件
- **monitor/monitor.go**: 新增事件去重配置（enableDedup、dedupWindowHours），防止重复处理同一事件
- **monitor/monitor.go**: 新增 API 冷却配置（enableRateLimit、rateLimitMs），防止触发 115 接口频率限制
- **monitor/monitor.go**: 新增重试配置（maxRetries、retryDelayMs），增强网络异常时的健壮性

### 文档更新

- 更新配置项参考.md，新增 download 子项配置说明（strmMaxConcurrent、autoDownloadMetadata）
- 更新配置项参考.md，新增 lifeMonitor 去重/冷却/重试配置说明

---

## v0.9.1 (2026-08-19)

### 新功能

- 完成 Go 语言重构，移除 Node.js 和 Nginx 依赖
- 实现 115 扫码登录流程
- 实现 Cookie 内存优先 + 异步刷盘架构
- 实现事件驱动 Cookie 验证机制
- 实现 115 生活事件监控
- 实现 STRM 文件自动生成与清理
- 实现 Emby 主动刷库功能
- 实现 Telegram 通知系统
- 实现通用 Webhook 多渠道通知
- 实现 302 直连模式
- 实现 UUID 缓存清理功能

### 优化

- UI 统一为黑白风格
- 前端升级为 Vite + React
- 后端框架升级为 Go + go-zero
- 并发安全控制
- API 冷却控制
