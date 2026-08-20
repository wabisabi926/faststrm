# FastStrm 变更日志

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