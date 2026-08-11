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

### Q: 如何修改/重置登录密码？

A: 登录后进入「设置」→ 修改密码。或直接编辑 `config/config.json`：

- **修改**：将 `password` 改为新密码的 SHA-256 哈希
- **重置为 admin**：将 `password` 改为 `8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918`
- 修改后重启容器生效

### Q: 如何完全重置？

A:
```bash
docker-compose down
rm -rf config/ data/ logs/
docker-compose up -d
```
⚠️ 会丢失所有配置和数据。

---

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

### Q: 中文路径在 Kodi 中显示乱码？

A: 开启 `enablePathEncoding: true`，URL 路径会被 `encodeURI` 编码。

---

## 生活事件监控

### Q: 监控不工作？

A: 排查步骤：
1. 确认「启用监控」已勾选
2. 确认账号已添加到监控列表
3. 确认路径映射正确（cloudPath 必须是 115 网盘的真实路径）
4. 查看日志是否有 `[LifeMonitor]` 错误
5. 检查 `lifeMonitorState.json` 断点是否过期

### Q: 删了网盘文件但本地 STRM 没删？

A: 可能原因：
- 删除事件触发熔断（单次 >100 条或占比 >50%）
- 文件不在路径映射范围内
- 删除事件被去重机制跳过（60 秒内重复）

手动解决：执行「STRM 清理」→「孤儿扫描」。

### Q: 监控卡住不动？

A: 可能是全量扫描未正常结束导致监控被暂停。等待 10 分钟会自动恢复，或重启容器。

---

## STRM 路由策略

### Q: Infuse 播放时拖动进度条卡死？

A: faststrm 已对 Infuse 强制走 proxy 模式。如果仍卡死：
1. 确认 UA 中包含 `Infuse`
2. 检查部署设备上行带宽是否足够
3. 尝试在私网用 `?mode=proxy` 强制指定

### Q: 想让所有请求都走 302（不中转）？

A: 当前默认就是 302。force-proxy UA 列表会强制 proxy，如需移除需编辑 `route.ts` 中的 `FORCE_PROXY_UA_TOKENS` 数组。

---

## Emby 集成

### Q: Emby 媒体库不自动刷新？

A: 排查：
1. 确认 Emby URL 和 API Key 正确
2. 确认 `libraryId` 配置正确（留空则自动通过路径匹配）
3. 查看 faststrm 日志是否有 Emby API 调用错误

### Q: 删除同步误删了文件？

A: 从 `.trash/` 回收站恢复：
1. 进入容器的 `data/.trash/` 目录
2. 找到对应日期子目录
3. 移回原位置
4. 重新执行任务或全量对账

> 回收站保留 7 天，请尽快恢复。

---

## Telegram 通知

### Q: 收不到 TG 通知？

A: 排查：
1. 确认 Bot Token 和 Chat ID 正确
2. 浏览器访问 `https://api.telegram.org/bot<TOKEN>/getMe` 验证 Token
3. 检查网络是否能访问 `api.telegram.org`（国内需代理）

---

## 其他

### Q: 支持阿里云盘/百度网盘吗？

A: 当前仅支持 115 网盘和 OpenList。其他网盘暂无计划。

### Q: 如何备份数据？

A: 备份以下目录：
- `config/` - 配置文件
- `data/` - 数据文件

> 不需要备份 `logs/` 和 `data/.trash/`。

### Q: 内存占用高？

A: faststrm 内存主要来自：
- urlCache（512 条，约 50MB）
- reachableCache（256 条，约 10MB）
- SQLite filePathDb（取决于文件数）

如内存紧张，可在 `route.ts` 调小缓存容量。
