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
   - URL：`http://faststrm地址:3000/api/emby/webhook`
   - 事件勾选：项目新增、项目删除、播放开始、播放结束
3. （可选）配置 `webhookAuth` 令牌

### faststrm 端配置

| 字段 | 说明 |
|------|------|
| 入库通知 | 媒体新增时发 TG 通知 |
| 删除通知 | 媒体删除时发 TG 通知 |
| 播放通知 | 播放开始/结束时发 TG 通知 |
| 显示进度 | 播放通知包含进度百分比 |
| 显示简介 | 播放通知包含媒体简介 |

### Webhook 事件说明

| Emby 事件 | 英文标识 | 说明 |
|-----------|---------|------|
| 媒体入库 | `library.new` | 新增媒体文件 |
| 媒体删除 | `library.deleted` | 删除媒体文件 |
| 播放开始 | `playback.start` | 开始播放 |
| 播放暂停 | `playback.pause` | 暂停播放 |
| 播放结束 | `playback.stop` | 停止播放 |

---

## 删除同步

详细的删除同步配置和使用方法，请查看 [删除同步](删除同步) 页面。

---

## Jellyfin 支持

Webhook 格式与 Emby 类似，但事件字段略有差异。当前主要测试 Emby，Jellyfin 可能需要调整 webhook 解析逻辑。
