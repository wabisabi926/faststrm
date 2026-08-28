# Emby 集成

Fast Strm 与 Emby 的集成包含三部分：媒体库刷新、Webhook 通知、删除同步。

---

## 媒体库刷新

### 自动刷新

任务执行完成、生活监控检测到变动后，faststrm 会自动调用 Emby API 刷新对应媒体库：

1. 通过路径前缀匹配找到对应的 `LibraryId`
2. 调用 `POST /emby/Items/{id}/Refresh`
3. **防抖**：10 秒窗口内同一媒体库的多次刷新合并为一次

### 配置

进入「Emby 通知」页面：

| 字段 | 说明 |
|------|------|
| Emby URL | Emby 服务器地址，如 `http://192.168.1.10:8096` |
| API Key | Emby → 设置 → 高级 → API 密钥 |
| 媒体库 ID | 可选，留空时自动通过路径匹配 |

### 测试连接

配置完 Emby URL 和 API Key 后，点击「测试连接」按钮验证配置是否正确。

faststrm 会调用 Emby 的 `System/Info` 接口进行校验，并提供 **9 种错误分类提示**：

| 错误类型 | 说明 |
|----------|------|
| URL 缺失 | 未填写 Emby URL |
| API Key 缺失 | 未填写 API Key |
| 连接超时 | Emby 服务器不可达 |
| 认证失败 | API Key 错误 |
| 权限不足 | API Key 无管理员权限 |
| Emby 版本过低 | Emby 版本不兼容 |
| SSL 证书错误 | HTTPS 证书验证失败 |
| 网络错误 | DNS 解析失败等 |
| 未知错误 | 其他异常 |

### 保存设置

点击「保存」后，faststrm 使用 `POST /api/emby/settings` 接口进行**局部更新**，只修改 Emby 相关字段，不会覆盖其他配置（302 重定向、监控账号、TG Bot 等）。

> 💡 v0.8.1 修复：此前保存 Emby 设置会覆盖整个 settings.json，现已改为局部 patch 模式。

---

## Webhook 通知

### Emby 端配置

1. Emby → 设置 → 高级 → 插件 → 安装「Webhooks」
2. 添加 Webhook：
   - URL：`http://faststrm地址:8090/api/emby/webhook`
   - 事件勾选：项目新增、项目删除、播放开始、播放结束
3. （可选）配置 `webhookAuth` 令牌

### faststrm 端配置

| 字段 | 默认（v1.1.1+） | 说明 |
|------|------|------|
| 入库通知 | ✅ 开启 | 媒体新增时发 TG 通知 |
| 删除通知 | ✅ 开启 | 媒体删除时发 TG 通知 |
| 播放通知 | ✅ 开启 | 播放开始/暂停/结束时发 TG 通知 |
| 显示进度 | ✅ 开启 | 播放通知包含进度百分比 |
| 显示简介 | 关闭 | 播放通知包含媒体简介 |

### 刮削等待（v1.1.1 修复"空壳"通知）

Emby webhook 到达时，元数据刮削**经常还没完成**——详情 API 返回空的 Overview / Genres / People，导致通知里只有标题没有简介、演员表和评分。

faststrm 的处理流程：

```
Emby webhook 到达（media.added）
  ↓
轮询 Emby 详情 API（3s 间隔，最多 60s）
  ↓
检测 Overview / Genres / People 是否全部非空
  ↓ 是
合并详情字段到 webhook 数据 → 发完整通知
  ↓ 否（超时/详情获取失败）
降级用 webhook 自带字段 → 发兜底通知（不丢）
```

- 等待间隔：3 秒（之前 5 秒，v1.1.1 调优）
- 最长等待：60 秒（之前 5 分钟，v1.1.1 收紧——超过 60s 基本刮削挂了，兜底发）
- 完全自动：用户无需配置，只影响通知的丰富度，**不会丢通知**

### Webhook 事件说明

| Emby 事件 | 英文标识 | 说明 |
|-----------|---------|------|
| 媒体入库 | `library.new` | 新增媒体文件 |
| 媒体删除 | `library.deleted` | 删除媒体文件 |
| 播放开始 | `playback.start` | 开始播放 |
| 播放暂停 | `playback.pause` | 暂停播放 |
| 播放结束 | `playback.stop` | 停止播放 |

---

## Telegram 通知模板（v1.0.6 对齐参考项目）

所有 Emby 通知统一走 `FormatMessage` 三段式渲染（Title / Content / Metadata），全角冒号、字段稳定排序。

### 📚 入库通知（电影 / 剧集）

**格式：**
- **Title**：📚 Emby 电影入库通知 / 📚 Emby 剧集入库通知
- **Content**：片名（年份）/ 剧名（年份）
- **Metadata**：评分、类型、主演、入库时间、简介（无导演字段，对齐参考项目）
- **配图**：Backdrop 横版背景优先 → Primary 竖版海报兜底

**剧集合并**：同一剧集（SeriesId）10 秒窗口内多集入库自动合并，消息内含「入库季集」字段（如 S1E1-E10）。

### 🗑️ 删除通知（电影 / 剧集 / 季 / 集）

**格式：**
- **Title**：🗑️ Emby 媒体删除通知
- **Metadata**：删除时间；剧集额外含删除季集汇总
- **防抖合并**：剧集 / 季 / 集删除带缓冲窗口，避免批量刷屏

### 📺 播放通知（开始 / 暂停 / 停止）

| 事件 | Title emoji | 说明 |
|------|-------------|------|
| 播放开始 | 📺 | 包含用户、设备、时长、简介（受开关控制） |
| 播放暂停 | ⏸️ | 额外包含观看时长、播放进度（受开关控制） |
| 播放停止 | ⏹️ | **v1.0.6 更新**：由禁止符号 `⛔` 改为标准停止符号 `⏹️` |

**v1.0.6 更新要点：**
- **字段名**：播放进度 metadata 由「进度」改为「播放进度」，语义更清晰
- **配图策略**：播放通知只取 Primary（竖版海报），不再优先 Backdrop 横版图，与文字通知更协调
- **条件请求**：`PlaybackShowProgress` 与 `PlaybackShowOverview` 关闭时不请求 Emby 详情 API，减少不必要调用

**开关控制（Emby 设置页）：**

| 开关 | 默认 | 说明 |
|------|------|------|
| 入库通知（NotifyMediaAdded） | 关 | 电影 / 剧集入库时推送 TG |
| 删除通知（NotifyMediaRemoved） | 关 | 删除媒体时推送 TG |
| 播放通知（NotifyPlayback） | 关 | 开始 / 暂停 / 停止时推送 TG |
| 显示进度（PlaybackShowProgress） | 关 | 播放通知显示播放进度 / 总时长 |
| 显示简介（PlaybackShowOverview） | 关 | 播放通知显示剧情简介（长简介自动截断） |

**去重**：同一用户 + 同一设备 + 同一 Item 60 秒内不重复推送。

---

## 删除同步

详细的删除同步配置和使用方法，请查看 [删除同步](删除同步) 页面。

---

### 故障排查（v1.0.2 新增诊断日志）

如果 TG 没收到入库/播放通知：
1. 查看 `app.log` 是否有 `[emby/webhook] 收到 webhook: Event=...` 这行
   - **没有** → Emby 端 webhook 事件类型未勾选「新建项」/「播放」，或网络不通
   - **有但 Event 不匹配** → Emby 版本事件标识不同，检查日志中 Event 实际值
2. 确认 faststrm 设置页「入库通知」/「播放通知」开关已打开
3. 确认 Telegram 已配置且 enabled

---

## Jellyfin 支持

Webhook 格式与 Emby 类似，但事件字段略有差异。当前主要测试 Emby，Jellyfin 可能需要调整 webhook 解析逻辑。
