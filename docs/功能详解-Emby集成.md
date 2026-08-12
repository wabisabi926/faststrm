# 功能详解 - Emby 集成

## 概述

Fast Strm 与 Emby 的集成包含三部分：媒体库刷新、Webhook 通知、删除同步。

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

[📸 此处需截图：Emby 配置卡片]

## Webhook 通知

### Emby 端配置

1. Emby → 设置 → 高级 → 插件 → 安装「Webhooks」
2. 添加 Webhook：
   - URL：`http://faststrm地址:3000/api/emby/webhook`
   - 事件勾选：项目新增、项目删除、播放开始、播放结束
3. （可选）在 faststrm 配置 `webhookAuth` 令牌，Emby Webhook URL 加 `?token=xxx`

### faststrm 端配置

| 字段 | 说明 |
|------|------|
| 入库通知 | 媒体新增时发 TG 通知 |
| 删除通知 | 媒体删除时发 TG 通知（生活监控触发） |
| 播放通知 | 播放开始/结束时发 TG 通知 |
| 显示进度 | 播放通知包含进度百分比 |
| 显示简介 | 播放通知包含媒体简介 |

## 删除同步

### 功能说明

当你在 Emby 中删除媒体时，Emby 会推送 `library.deleted` 事件。faststrm 收到后自动删除本地 STRM + 关联文件 + DB 记录。

### 启用

进入「Emby 通知」页面 → 「删除同步」卡片：

1. 勾选「启用删除同步」
2. **首次启用建议勾选「试运行模式」**（只记日志不删除）
3. 配置路径映射
4. 保存

[📸 此处需截图：删除同步配置卡片]

### 路径映射

告诉 faststrm「Emby 中的这个路径」对应「115 网盘的哪个路径」：

```json
[
  {
    "embyPath": "/app/data/strm/电影",
    "cloudPath": "/电影",
    "account": "我的115"
  }
]
```

- `embyPath`：Emby webhook 推送的 `Item.Path` 前缀
- `cloudPath`：115 网盘对应路径
- `account`：可选，用于清理 filePathDb

### 决策链

```
Emby library.deleted 事件
  ↓
检查启用 → 未启用则跳过
  ↓
检查 Path 字段 → 缺失则跳过
  ↓
白名单匹配（路径前缀）→ 不匹配则跳过
  ↓
去重检查（60 秒窗口，防生活监控重复）→ 命中则跳过
  ↓
路径映射（路径段感知）→ Emby 路径 → 网盘路径
  ↓
防误删1：STRM 文件/目录存在才处理
  ↓
防误删2：标题校验（Movie/Episode 严格匹配）
  ↓
试运行检查 → 是则只记日志
  ↓
按类型删除：
  Movie/Episode → 删 STRM + 关联字幕/nfo/图片（文件级物理删除）
  Season → 删整季目录（目录级物理删除）
  Series → 删整剧目录（目录级物理删除）
  ↓
防误删3：目录文件数 ≤100 才删
  ↓
更新 filePathDb
  ↓
写删除历史 + TG 通知
```

### 三道防误删

| 防线 | 说明 |
|------|------|
| 防误删1 | STRM 文件/目录不存在则跳过（可能已被生活监控处理） |
| 防误删2 | Movie/Episode 标题必须匹配 STRM 文件名 |
| 防误删3 | 整季/整剧删除时，目录文件数 ≤100 才执行 |

### 去重机制

生活监控和 Emby webhook 可能同时触发同一文件的删除：
- 生活监控：115 网盘删了 → 删 STRM → Emby 发现 STRM 没了 → 推 `library.deleted`
- 60 秒窗口内，如果同一路径刚被生活监控处理过，Emby 的删除事件会被跳过

### 试运行模式

首次配置时强烈建议开启：
- 只记录日志，不实际删除
- 日志会显示「试运行模式，仅记录不删除: <路径>」
- 确认路径映射正确后再关闭

### 删除历史

`data/syncDelHistory.json` 保留最近 200 条删除记录：
```json
[
  {
    "itemPath": "/电影/小王子.mkv",
    "itemName": "小王子",
    "itemType": "Movie",
    "deletedAt": "2026-08-11T10:30:00.000Z",
    "deletedFiles": 3
  }
]
```

### 删除机制

所有删除操作均为**直接物理删除**（`fs.unlinkSync` / `fs.rmSync`），不经过回收站：

- **文件级删除**：STRM + 关联字幕/nfo/图片
- **目录级删除**：整季/整剧目录
- **不可本地恢复**：删除后无法从本地找回

> 误删后可在 115 网盘恢复文件后重新执行全量扫描生成 STRM。建议首次配置时开启「试运行模式」确认路径映射正确后再正式启用。

## Jellyfin 支持

Webhook 格式与 Emby 类似，但事件字段略有差异。当前主要测试 Emby，Jellyfin 可能需要调整 webhook 解析逻辑。
