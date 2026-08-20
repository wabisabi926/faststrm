# FastStrm 变更日志

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
