# 功能详解 - STRM 生成

## 概述

STRM 生成是 Fast Strm 的核心功能：扫描 115 网盘目录树，为每个媒体文件生成本地 `.strm` 文件，配合 Emby/Kodi 实现云盘资源本地化播放。

## 任务配置

### 创建任务

进入「任务管理」→「新建任务」：

| 字段 | 必填 | 说明 |
|------|------|------|
| 账号 | ✓ | 选择已添加的 115 账号 |
| 云端目录 | ✓ | 115 网盘源路径，如 `/电影` |
| 本地目录 | ✓ | STRM 保存子目录，如 `电影` |
| STRM 前缀 | - | 任务级覆盖（留空用全局） |
| 定时任务 | - | cron 表达式，如 `0 3 * * *` |
| 删除多余文件 | - | 远端不存在时删本地 STRM |
| 是否生成 STRM | - | 关闭则只下载文件不生成 STRM |
| 下载扩展名 | - | 任务级覆盖 |
| STRM 扩展名 | - | 任务级覆盖 |

[📸 此处需截图：新建任务对话框]

### STRM 扩展名默认值（v1.1.1 新增 iso）

| 类型 | 格式 |
|------|------|
| 常规视频 | mp4, mkv, avi, mov, rmvb, webm, flv, m3u8, ts, m2ts |
| 音频 | mp3, flac, wav, aac, ogg, m4a |
| 原盘 | **iso**（v1.1.1 新增蓝光原盘支持） |

### ISO 原盘 STRM 格式

对于 `.iso` 原盘文件，生成的 STRM 采用**双扩展名**：

```
源文件: /电影/2024/星际穿越.iso
            ↓
STRM:  /电影/2024/星际穿越.iso.strm
内容:  http://服务器:8090/api/strm?account=xxx&pickcode=xxx&file_name=星际穿越.iso
```

为什么双扩展名？Emby/Jellyfin 看到 `.iso.strm` 就知道"这是一个 ISO 原盘的 STRM 代理"，播放时会按原盘模式（支持菜单、章节、音轨切换）而不是普通视频模式。

### STRM 文件名模板

默认模板：`{stem}.strm`（iso 自动变 `{stem}.iso.strm`）

支持的变量：

| 变量 | 含义 | 示例 |
|------|------|------|
| `{filename}` | 完整文件名（含扩展名） | `星际穿越.iso` |
| `{stem}` | 去掉最后一个扩展名的文件名 | `星际穿越.iso`（iso 的 stem 保留 iso 后缀） |
| `{ext}` | 扩展名（不含点） | `iso` |

通过任务级「STRM 文件名模板」覆盖，或全局配置 `settings.json → strm.strmFilenameTemplate`。

**v1.2.3+ 统一 STRM 模式**：
- `strmPrefix = http://服务器:8090`（不含 `/api/strm`，前端自动追加）
- STRM 内容：`http://服务器:8090/api/strm?account=xxx&pickcode=xxx&file_name=xxx`
- 播放时 faststrm 接收请求 → 走[智能路由策略](STRM-路由策略)决策（ISO 自动 proxy，mkv/mp4 默认 redirect）
- **依赖 pickcode**：必须有 `pickcode` 才能生成有效 STRM

> **v1.2.3 架构变更**：旧版 `enable302=false` 的 OpenList 直链模式已废弃（不再生成非 query URL）。所有 STRM 统一经过 faststrm 路由层，获得 force-proxy、并发限流、可达性预检等能力。

### 路径映射

任务通过 `云端目录` + `本地目录` 建立映射：

```
115: /电影/2024/小王子.mkv
          ↓
本地: /app/data/strm/电影/2024/小王子.mkv.strm
```

- `本地目录` 是 `exportDir`（STRM 根目录）下的子目录
- 完整本地路径 = `{exportDir}/{本地目录}/{相对云端路径}`

### 定时任务

cron 表达式示例：

| 表达式 | 含义 |
|--------|------|
| `0 3 * * *` | 每天凌晨 3 点 |
| `0 */6 * * *` | 每 6 小时 |
| `0 0 * * 0` | 每周日凌晨 |
| 留空 | 仅手动执行 |

> 账号级令牌桶限流：同一账号的多个任务会自动排队，避免 115 API 频控。

## 任务执行流程

```
任务触发
  ↓
suspendMonitorForFullScan(account)  暂停生活监控（防竞争）
  ↓
递归扫描云端目录
  ↓
对比本地 STRM + filePathDb
  ↓
新增：生成 STRM + 写 DB
删除：直接物理删除 + 删 DB
更新：覆盖 STRM + 更新 DB
  ↓
清理空目录（removeEmptyParents）
  ↓
clearMonitorSuspend(account)  恢复生活监控
  ↓
Emby 媒体库刷新（防抖）
  ↓
TG 通知
```

## pickcode 获取机制

302 模式下，`pickcode` 是 STRM 的核心凭证（115 文件唯一标识）。获取方式因生成途径而异：

### 生活事件生成

115 生活事件 API 直接返回 `pick_code` 字段，100% 可靠：

```
115 → life event → pick_code → generateStrmContent → 写入 STRM
                                                → upsertFilePathEntry（存 DB）
```

### 全量扫描生成

全量扫描时 `exportDirParse` 只返回文件列表（不含 pickcode），需要从 `filePathDb` 反查：

```
全量扫描 → getFilePathEntryByPath(account, cloudPath)
         → 命中：DB 已有记录 → 取 pickcode → 生成 STRM
         → 未命中：跳过生成，输出警告日志
```

**首次全量扫描**时 DB 可能为空，`pickcode` 无法获取。执行一次全量扫描后，后续扫描可正常反查。

### 兜底保护

当 `pickcode` 缺失时：

1. `generateStrmContent` 返回空字符串 + `console.warn` 警告
2. 调用方（`enqueueForAccount` / `strmCleanup`）跳过写入
3. `syncStrmText` 防御性检查，拒绝写入空内容
4. 日志中可看到：`[STRM] pickcode 缺失，跳过生成`

## STRM 文件格式

每行一个 URL，UTF-8 编码：

```
http://192.168.1.100:8090/api/strm?account=我的115&pickcode=xxxx&file_name=小王子.mkv
```

- `account`：账号名
- `pickcode`：115 文件唯一标识
- `file_name`：文件名（用于 Content-Disposition 和大文件判断）

## 路径编码

`enablePathEncoding: true` 时，URL 中的中文/特殊字符会被 `encodeURI`：
- 关：`/电影/小王子.mkv`
- 开：`/%E7%94%B5%E5%BD%B1/%E5%B0%8F%E7%8E%8B%E5%AD%90.mkv`

> Kodi 某些版本对非编码中文路径支持不好，建议开启。

## 删除多余文件

`removeExtraFiles: true` 时：
- 扫描时发现本地 STRM 在远端不存在 → 直接物理删除
- 同时清理 DB 中对应记录
- 误删后可在 115 网盘恢复文件后重新全量扫描生成 STRM

## 性能参考

| 目录规模 | 耗时（参考） |
|----------|-------------|
| 100 文件 | < 5 秒 |
| 1000 文件 | 30 秒 |
| 10000 文件 | 5 分钟 |
| 100000 文件 | 30+ 分钟 |

> 大目录建议拆分多个任务，避免单任务超时。
