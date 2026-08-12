# FAQ - 常见问题

## 安装部署

### Q: Docker 镜像支持哪些平台？

A: `linux/amd64`（x86_64）和 `linux/arm64`（Apple Silicon / 树莓派 4 / CoreELEC）。

### Q: 如何升级版本？

A:
```bash
docker-compose pull
docker-compose up -d
```
配置文件向后兼容，无需手动迁移。

### Q: 如何修改登录密码？

A: 登录后进入「设置」→ 修改密码。或直接编辑 `config/config.json`（密码使用 salt+SHA-256 哈希存储）后重启容器。

### Q: 忘记密码怎么办？

A: 停止容器 → 编辑 `config/config.json` → 把 `password` 改为明文 `admin`（代码对旧格式有明文兼容）→ 重启容器 → 用 `admin/admin` 登录 → 立即在「设置」页面修改密码（会自动写入 salt+SHA-256 哈希）。

> ⚠️ 不要直接填纯 SHA-256 值（如 `8c6976e5...`），当前密码使用 salt+SHA-256 机制，纯哈希值无法通过验证。

## STRM 生成

### Q: STRM 文件生成后播放不了？

A: 排查步骤：
1. 检查 `strmPrefix` 是否正确（能从播放器访问 faststrm）
2. 检查 115 账号 Cookie 是否过期（账号管理页面有状态提示）
3. 查看 faststrm 日志是否有 `[STRM]` 相关错误
4. 如果是 302 模式，确认播放器支持 302 重定向

### Q: Cookie 经常过期怎么办？

A: 115 Cookie 有效期约 7-30 天。建议：
- 定期更新 Cookie
- 不要在多个地方同时登录同一 115 账号（会互相挤掉）
- 考虑使用 115 的 API Key（如果可用）

### Q: 任务执行很慢？

A: 可能原因：
- 目录文件数过多（>10000 建议拆分任务）
- 115 API 频控（令牌桶限流会自动排队）
- 网络延迟

### Q: 中文路径在 Kodi 中显示乱码？

A: 开启 `enablePathEncoding: true`，URL 路径会被 `encodeURI` 编码。

## 生活事件监控

### Q: 监控不工作？

A: 排查步骤：
1. 确认「启用监控」已勾选
2. 确认账号已添加到监控列表
3. 确认路径映射正确（cloudPath 必须是 115 网盘的真实路径）
4. 查看日志是否有 `[LifeMonitor]` 错误
5. 检查 `lifeMonitorState.json` 断点是否过期（超过 7 天会重置）

### Q: 删了网盘文件但本地 STRM 没删？

A: 可能原因：
- 删除事件触发熔断（单次 >100 条或占比 >50%），检查日志是否有 `熔断` 字样
- 文件不在路径映射范围内
- 删除事件被去重机制跳过（60 秒内重复）

手动解决：执行「STRM 清理」→「孤儿扫描」。

### Q: 监控卡住不动？

A: 可能是全量扫描任务未正常结束导致监控被暂停（`suspendMonitorForFullScan`）。等待 10 分钟（`FULLSCAN_TIMEOUT_MS`）会自动恢复，或重启容器。

## STRM 路由策略

### Q: Infuse 播放时拖动进度条卡死？

A: faststrm 已对 Infuse 强制走 proxy 模式。如果仍卡死：
1. 确认 UA 中包含 `Infuse`（查看日志 `decision=proxy reason=force_proxy_ua:Infuse`）
2. 检查部署设备上行带宽是否足够（proxy 模式走服务器中转）
3. 尝试 `?mode=proxy`（仅私网）强制指定

### Q: 想让所有请求都走 302（不中转）？

A: 当前默认就是 302。force-proxy UA 列表（Infuse/VidHub/SenPlayer）会强制 proxy，如需自定义可在「设置」页面 → STRM 路由策略 → `forceProxyUaTokens` 中调整（对应 `settings.json → strm.forceProxyUaTokens`），修改实时生效无需重启。或在私网用 `?mode=redirect` 指定。

### Q: 115 单账号播放报错「并发过多」？

A: 这是 115 的约 10 进程上限。faststrm 已有并发限流（默认阈值 8），如仍触发：
- 减少同时播放的设备数
- 考虑使用多个 115 账号分流
- 在「设置」页面 → STRM 路由策略 → `accountProxyConcurrencyLimit` 调小阈值（对应 `settings.json → strm.accountProxyConcurrencyLimit`）

## Emby 集成

### Q: Emby 媒体库不自动刷新？

A: 排查：
1. 确认 Emby URL 和 API Key 正确
2. 确认 `libraryId` 配置正确（留空则自动通过路径匹配）
3. 查看 faststrm 日志是否有 Emby API 调用错误
4. Emby 的 `Items/{id}/Refresh` 可能需要管理员权限

### Q: 删除同步不工作？

A: 排查：
1. 确认 Emby Webhook 已配置并勾选「项目删除」
2. 确认 faststrm 的「删除同步」已启用
3. **首次建议开启试运行模式**，查看日志确认路径映射正确
4. 路径映射的 `embyPath` 必须与 Emby 推送的 `Item.Path` 完全匹配前缀

### Q: 删除同步误删了文件？

A: STRM 删除为直接物理删除，无回收站。恢复方式：
1. 在 115 网盘客户端确认源文件是否存在
2. 若源文件存在，重新执行对应任务或全量对账即可重新生成 STRM

> 建议首次启用删除同步时开启「试运行模式」确认路径映射正确，避免误删。

## Telegram 通知

### Q: 收不到 TG 通知？

A: 排查：
1. 确认 Bot Token 和 Chat ID 正确
2. 确认 Bot 已加入目标群组（群组通知场景）
3. 浏览器访问 `https://api.telegram.org/bot<TOKEN>/getMe` 验证 Token
4. 检查网络是否能访问 `api.telegram.org`（国内需代理）

### Q: 剧集入库时通知刷屏？

A: faststrm 有 10 秒缓冲合并机制。如仍刷屏：
- 确认 Emby webhook 推送了 `SeriesId`（用于合并判断）
- 调整缓冲窗口（需改代码）

## 分享链接转存

### Q: 转存成功但没生成 STRM？

A: 可能原因：
- 没有匹配的任务（需同账号 + 任务路径包含转存目标目录）
- `getPathByCid` 路径反查失败（已知缺陷）
- 转存目标目录无视频文件

手动解决：在任务管理中手动执行对应任务。

### Q: 转存大文件夹很慢？

A: 当前转存是批量提交 fileIds，无分页。大文件夹建议：
- 分批勾选转存
- 或直接在 115 网盘客户端转存后，用 faststrm 任务扫描生成 STRM

## 性能与资源

### Q: 内存占用高？

A: faststrm 内存占用主要来自：
- urlCache（512 条，约 50MB）
- reachableCache（256 条，约 10MB）
- SQLite filePathDb（取决于文件数）

> 缓存容量（`URL_CACHE_TTL` / `URL_CACHE_SIZE` / `REACHABLE_CACHE_TTL` / `REACHABLE_CACHE_SIZE`）保持内部常量，未暴露到 `settings.json`。如确需调整，请修改源码后重新构建。

### Q: 磁盘占用增长？

A: 主要来自：
- `logs/` 日志（建议定期清理或配置 logrotate）
- `filePathDb.sqlite`（随文件数增长，可定期 VACUUM）

## 其他

### Q: 支持阿里云盘/百度网盘吗？

A: 当前仅支持 115 网盘和 OpenList。阿里云盘/百度网盘暂无计划。

### Q: 支持 Jellyfin 吗？

A: Webhook 格式与 Emby 类似，但事件字段略有差异。当前主要测试 Emby，Jellyfin 可能需要调整 webhook 解析逻辑。

### Q: 如何备份数据？

A: 备份以下目录：
- `config/` - 配置文件
- `data/` - 数据文件（filePathDb、历史记录等）

> 不需要备份 `logs/`。

### Q: 如何完全重置？

A:
```bash
docker-compose down
rm -rf config/ data/ logs/
docker-compose up -d
```
> 会丢失所有配置和数据，谨慎操作。
