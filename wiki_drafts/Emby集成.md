# 功能详解 - Emby 集成

## 概述

Fast Strm 与 Emby 的集成包含四部分：**媒体库刷新**、**Webhook 通知**、**删除同步**、**反向代理（PlaybackInfo 拦截）**。

## Emby 反向代理（v1.1.8+ 推荐开启）

### 为什么需要反向代理？

**问题**：Emby 对 STRM 远程文件默认强制转码（认为远程 HTTP URL 不安全），导致：

- Emby for Kodi 播放 STRM 时报「没有兼容的流」
- Emby Web 播放 ISO 原盘被强制作 HLS 转码，4K 卡顿
- Kodi 无法使用原生 Libdvd 菜单 / 章节功能

**解决方案**：FastStrm 提供一个轻量级 Emby 反向代理（embyproxy），拦截 PlaybackInfo API 响应，识别 STRM 源文件（`IsRemote=true` + `Protocol=Http`）并**强制改写为 DirectPlay**，同时移除转码相关字段。

### 工作原理

```
┌──────────────┐       ┌───────────────────────┐       ┌──────────────┐
│ Emby for Kodi │──────▶│ FastStrm EmbyProxy     │──────▶│ Emby Server   │
│ (或 Emby Web) │ :8097 │ (拦截 PlaybackInfo)    │       │ :8096         │
└──────────────┘       └───────────────────────┘       └──────────────┘
                              │
                    POST /Items/{id}/PlaybackInfo
                    识别 STRM 源 → 强制 SupportsDirectPlay=true
                    移除 TranscodingUrl 等转码字段
                    缓存 MediaSourceId → STRM URL 映射
```

### 启用步骤

1. 进入「Emby 通知」页面 → 「Emby 反向代理」卡片
2. 勾选「启用 Emby 反向代理」
3. **代理端口**默认 `8097`（确保不与主服务 `8090` 和 Emby `8096` 冲突）
4. 填写 Emby Server 地址（如 `http://192.168.1.10:8096`）
5. **保存即可** —— v1.2.4 起支持**热重启**，改端口 / 改 Emby 地址 / 关闭反代全部**即时生效**，不用重启 faststrm 主程序
6. 查看 faststrm 日志确认启动：
   ```
   [EmbyProxy] 启动中: 0.0.0.0:8097 → http://192.168.1.10:8096
   [EmbyProxy] 请将 Emby 客户端连接到 http://192.168.1.10:8097
   [EmbyProxy] STRM ISO/MKV 自动强制 DirectPlay，绕过 Emby 转码限制
   ```

### Emby 客户端配置

#### Emby for Kodi / Emby for Android TV

服务器地址改为指向 **FastStrm 代理端口**（而不是 Emby 原生端口）：

```
Emby 服务器地址: http://192.168.1.10:8097
```

> Emby Server 原生端口（默认 8096）保持不变，faststrm 反代会把所有请求透传给 Emby，只修改 PlaybackInfo 响应。

#### Emby Web

直接在浏览器访问 `http://192.168.1.10:8097` 就能使用反代的 Emby Web 界面。

### 功能范围

| API | 行为 |
|-----|------|
| `POST /Items/{id}/PlaybackInfo` | **拦截** — STRM 源强制 DirectPlay，缓存 MediaSourceId |
| `GET /MediaStream/{id}.{container}` | **302 重定向** — 返回 STRM 文件 URL，交由 `/api/strm` 处理 |
| 其他所有 Emby API | **原样透传** — 登录、刮削、通知、管理等完全不受影响 |

### 日志验证

播放 STRM 时 faststrm 日志会显示：
```
[EmbyProxy] PlaybackInfo 强制 DirectPlay: path=/emby/Items/123/PlaybackInfo, sources=2
[STRM] account=我的115 pickcode=csv7…567 decision=proxy reason=format_force_proxy:iso
```

### 端口说明

| 端口 | 服务 | 说明 |
|------|------|------|
| `8090`（默认） | FastStrm 主服务 | Web UI + 任务管理 + STRM 路由 |
| `8096`（默认） | Emby Server | Emby 原生端口，**不需要改** |
| `8097`（默认） | FastStrm EmbyProxy | Emby 反代，**客户端连接此端口** |

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

[📸 此处需截图：Emby 配置卡片]

## Webhook 通知

### Emby 端配置

1. Emby → 设置 → 高级 → 插件 → 安装「Webhooks」
2. 添加 Webhook：
   - URL：`http://faststrm地址:8090/api/emby/webhook`
   - 事件勾选：项目新增、项目删除、播放开始、播放结束
3. （可选）在 faststrm 配置 `webhookAuth` 令牌，Emby Webhook URL 加 `?token=xxx`

### faststrm 端配置

| 字段 | 默认（v1.1.1+） | 说明 |
|------|------|------|
| 入库通知 | ✅ 开启 | 媒体新增时发 TG 通知 |
| 删除通知 | ✅ 开启 | 媒体删除时发 TG 通知（生活监控触发） |
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
