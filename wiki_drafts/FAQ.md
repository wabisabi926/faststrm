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

### Q: 从 v1.2.2 升级到 v1.2.3 需要注意什么？

A: v1.2.3 有一个**架构变更**：统一 STRM 端点、废弃 `enable302` 开关。但升级**无需手动操作**，完全向后兼容：

- ✅ 旧 STRM 文件（引用 `/api/fs/get`）自动转发兼容，播放不受影响
- ✅ `settings.json` 里残留的 `enable302` 字段会被忽略，不会报错
- ✅ 任务配置里的 `enable302` 同样忽略

**可选清理**（不影响运行）：
```bash
# Linux / Mac
bash scripts/rebuild_strm.sh -w /path/to/your/strm/

# Windows PowerShell
.\scripts\rebuild_strm.ps1 -w -DirPath "D:\Media\strm"
```
跑一遍后，所有 STRM 的 `/api/fs/get` 会被批量替换成 `/api/strm`，清理冗余。默认干跑预览，加 `-w` 才真写。

### Q: 如何修改/重置登录密码？

A: 登录后进入「设置」→ 修改密码。或直接编辑 `config/config.json`：

- **修改**：登录后通过「设置」页面修改（自动写入 salt+SHA-256 哈希）
- **重置为 admin**：将 `password` 改为明文 `admin`（代码对旧格式有明文兼容），重启后用 `admin/admin` 登录，再进「设置」修改密码
- 修改后重启容器生效

> ⚠️ 不要直接填纯 SHA-256 值（如 `8c6976e5...`），当前密码使用 salt+SHA-256 机制，纯哈希值无法通过验证。

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
2. 检查 115 账号 Cookie 是否过期（账号管理页面有状态提示，v1.2.3+ 新增 **Deep Check 真实 API 验证**，不再只看格式）
3. 查看 faststrm 日志是否有 `[STRM]` 相关错误
4. v1.2.3 起所有 STRM 统一走 `/api/strm`，智能路由自动决策 redirect 或 proxy

### Q: Cookie 经常过期怎么办？

A: 115 Cookie 有效期约 7-30 天。v0.8.2 起推荐方案：
- 账号管理页面点击「更新 Cookie」按钮，扫码即可刷新（无需手动复制粘贴）
- 开启账户状态 TG 通知（`telegram.accountAlerts`），Cookie 异常时第一时间推送告警
- 不要在多个地方同时登录同一 115 账号（会互相挤掉）

### Q: 扫码登录支持哪些客户端？（v0.8.2 新增）

A: 支持以下 7 种客户端类型（对齐 p115client `APP_TO_SSOENT`）：
- 支付宝小程序（默认，VIP 推荐，不会踢掉现有设备）
- 微信小程序
- 115 安卓
- 115 iOS
- 115 TV
- 115 网页（⚠️ 会踢掉网页端登录，非必要不选）
- 115 管理端

扫码后自动回填 Cookie 并校验有效性，无需手动复制粘贴。

### Q: 想第一时间知道 Cookie 过期？（v0.8.2 新增）

A: 在「Telegram 通知」页面开启「账户状态通知」：
1. 配置 Telegram Bot（如未配置见 [Telegram 通知](Telegram-通知)）
2. 勾选「启用账户状态通知」

异常时推送告警，扫码刷新后自动推送恢复通知，形成闭环。详见 [配置参考](配置说明) 的 `telegram.accountAlerts` 段。

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

### Q: 115 生活监控 STRM 不生成？（v1.0.2 修复）

A: v1.0.2 修复了一个关键 bug：早期版本默认调用 `webapi.115.com/behavior/detail`，该端点返回的事件 `pick_code` 字段经常缺失，导致单文件创建事件被全部跳过。v1.0.2 已改为默认走 `proapi.115.com/ios/behavior/detail`（字段完整）。

如果升级后仍不生成，排查：
1. 确认路径映射正确（cloudPath 必须是 115 网盘真实路径）
2. 查看日志是否有 `invalid_pickcode` 或 `pick_code missing` 提示
3. 检查文件是否被扩展名/大小/黑名单过滤
4. 查看日志是否有 `[Monitor] ... poll summary` 确认事件已拉取

### Q: 删了网盘文件但本地 STRM 没删？

A: 可能原因：
- 删除事件触发熔断（单次 >100 条或占比 >50%）
- 文件不在路径映射范围内
- 删除事件被去重机制跳过（60 秒内重复）

手动解决：执行「STRM 清理」→「孤儿扫描」。

### Q: 监控卡住不动？

A: 可能是全量扫描未正常结束导致监控被暂停。等待 10 分钟会自动恢复，或重启容器。

### Q: 服务重启后还要手动启动监控吗？（v0.8.2 新增）

A: 不需要。v0.8.2 起监控支持自动启动，需同时满足以下条件：

1. 「启用监控」已勾选（`lifeMonitor.enabled = true`）
2. `config.accounts` 非空（至少配置一个账号）
3. 对应账号凭据齐全（115 账号需 cookie 长度 > 0，openlist 账号需 url、account、password 齐全）

满足上述条件时，服务重启后会自动拉起监控，无需手动操作。

> Cookie 失效不会阻止自动启动，监控进程启动后会通过一次轮询检测 Cookie 有效性并标记异常状态。

---

## STRM 路由策略

### Q: Infuse 播放时拖动进度条卡死？

A: faststrm 已对 Infuse 强制走 proxy 模式。如果仍卡死：
1. 确认 UA 中包含 `Infuse`
2. 检查部署设备上行带宽是否足够
3. 尝试在私网用 `?mode=proxy` 强制指定

### Q: 想让某些文件不走代理、直连 115 CDN？

A: 默认就是直连 302 重定向。只有以下情况才走代理（v1.2.3 智能路由自动决策）：
- 文件扩展名是 ISO / BDMV / M2TS / VOB / TS（需要 Range seek）
- 播放器 UA 在 `forceProxyUaTokens` 列表中（Infuse / VidHub / SenPlayer 等 seek 兼容性差的客户端）

如需调整，在「设置」页面 → STRM 路由策略 → 修改 `forceProxyUaTokens`。修改实时生效无需重启。

---

## Emby 集成

### Q: Emby for Kodi 播放 ISO 原盘报「没有兼容的流」或 Libdvd 无法 seek？

A: 这是 Emby for Kodi 的 PlaybackInfo 强制转码导致的。解决方案：

1. 进入「Emby 通知」页面 → 「Emby 反向代理」卡片
2. **勾选启用**，代理端口默认 `8097`
3. 在 Emby for Kodi 中，服务器地址改为 `http://你的faststrm地址:8097`（原生 Emby 端口 8096 保持不变）

embyproxy 会拦截 PlaybackInfo API，识别 STRM 源文件并强制 DirectPlay，ISO 原盘就能正常用 Libdvd 菜单播放了。详见 [Emby 集成 - 反向代理](Emby集成#emby-反向代理v118-推荐开启)。

### Q: Emby 媒体库不自动刷新？

A: 排查：
1. 确认 Emby URL 和 API Key 正确
2. 确认 `libraryId` 配置正确（留空则自动通过路径匹配）
3. 查看 faststrm 日志是否有 Emby API 调用错误

### Q: 删除同步误删了文件？

A: STRM 删除为直接物理删除，无回收站。恢复方式：
1. 在 115 网盘客户端确认源文件是否存在
2. 若源文件存在，重新执行对应任务或全量对账即可重新生成 STRM

> 建议首次启用删除同步时开启「试运行模式」确认路径映射正确，避免误删。

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

> 不需要备份 `logs/`。

### Q: 内存占用高？

A: faststrm 内存主要来自：
- urlCache（512 条，约 50MB）
- reachableCache（256 条，约 10MB）
- SQLite filePathDb（取决于文件数）

> 缓存容量（`URL_CACHE_TTL` / `URL_CACHE_SIZE` / `REACHABLE_CACHE_TTL` / `REACHABLE_CACHE_SIZE`）保持内部常量，未暴露到 `settings.json`。如确需调整，请修改源码后重新构建。
