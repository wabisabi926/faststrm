# Emby 通知与 Webhook

## `POST /api/emby/webhook`

公开回调（无 JWT）。用于接收 Emby Webhooks 插件的媒体与播放事件。

鉴权密钥在「设置 → 通知中心 → Emby」配置，**必填**；未配置时所有回调返回 401。支持三种传法：

- 查询参数：`?auth=<密钥>`（推荐）
- 请求头：`X-Webhook-Auth: <密钥>`
- 请求头：`Authorization: Bearer <密钥>`

旧版 Wiki 的 `?token=<密钥>` 仍然兼容，新配置请使用 `?auth=`。

支持的事件：

| 事件 | 行为 |
|---|---|
| `library.new` | 入库通知（依赖 Telegram） |
| `library.deleted` | 删除通知；启用删除同步时同时清理 STRM / 关联文件 / DB 记录 |
| `playback.start` / `playback.pause` / `playback.stop` | 播放通知 |

通知统一通过 Telegram 发送；未配置 Telegram 时 Webhook 仍会被接收，但不会推送消息。

## 刷库

启用「入库/删除后刷新 Emby」时，刷库按目录防抖并限制并发：

- 同一目录只保留一个防抖 timer，默认 10s（`emby.debounceSeconds` 可调）；
- 刷新父目录时 `Recursive=true`，覆盖其下新增/删除项；
- 同时最多 4 个刷新请求，排队超过 30s 的目录跳过并记录警告，避免全量任务把 Emby 打满。

全量任务只对发生变化的目录刷库，不会为每个文件单独请求 Emby。

## `GET /api/emby/settings`

JWT 保护。返回 `EmbySettings`，其中 `apiKey` 已脱敏。

## `POST /api/emby/settings`

JWT 保护。只提交需要修改的字段（patch 语义）；保存后热更新 Notifier、反代和外部播放器注入。

常用字段：

| 字段 | 说明 |
|---|---|
| `url` / `apiKey` | Emby 地址与 API Key |
| `webhookAuth` | Webhook 鉴权密钥（必填） |
| `proxyPort` | 反代端口，`0` 表示关闭 |
| `externalPlayerEnabled` | 详情页外部播放器按钮 |
| `notifyMediaAdded` / `notifyMediaRemoved` / `notifyPlayback` | 通知开关 |
| `syncDeleteEnabled` / `syncDeleteDryRun` / `syncDeleteNotify` | 删除同步与试运行 |
| `syncDeletePathMappings` | Emby 路径 → 云端路径映射 |

反代启动失败时仍返回 200，并附带 `proxyError` 与可读 `message`，前端会显示错误但保留已保存的设置。

## `POST /api/emby/test-connection`

JWT 保护。Body 或 query 传 `url` / `apiKey`；页面显示脱敏 Key 且未重新输入时返回失败提示。返回 `{success, message, detail?}`。

## 边界

- 通用 `/api/settings` **不写入** Emby/Telegram 配置；请使用本页接口或「设置 → 通知中心」。
- Webhook 密钥、Bot Token、API Key 不会出现在日志或返回体中（API Key 仅返回脱敏值）。
- 删除同步默认先试运行：未显式配置过 `syncDeleteDryRun` 且未启用删除同步时默认为 `true`。
