# 功能详解 - STRM 路由策略

## 概述

当 `enable302: true` 时，播放器请求 faststrm 的 `/api/strm` 接口，faststrm 根据**规则引擎**决定走 302 重定向还是代理中转。

## 两种模式

### redirect（302 直连）

```
播放器 → faststrm → 302 + Location: 115 CDN URL → 播放器直连 115 CDN
```

- faststrm 不中转流量，部署设备零带宽消耗
- 依赖播放器正确处理 302 + Range
- 适合 Emby Server / Kodi / 浏览器等兼容性好的客户端

### proxy（代理中转）

```
播放器 → faststrm → fetch(115 CDN) → 流式回传播放器
```

- faststrm 中转流量，占用部署设备上行
- 服务器端可控（可做超时、重试、直链刷新）
- 适合 Infuse/VidHub/SenPlayer 等 seek 兼容性差的客户端

## 规则引擎（决策优先级）

```
请求到达 /api/strm?account=xxx&pickcode=xxx&file_name=xxx
  ↓
规则0: 校验 pickcode 格式（17 位字母数字）→ 400 非法
  ↓
规则1: ?mode=xxx（仅私网生效）→ 用户强制指定
  ↓
规则2: UA 匹配 force-proxy 列表？→ 强制 proxy
  ↓
规则3: 默认 → redirect（302 直连）
  ↓
兜底1: redirect 但 HEAD 预检失败（CDN 不可达）→ 降级 proxy
兜底2: proxy 但单账号并发 ≥ 上限 → 切 redirect
```

### 规则 0：pickcode 校验

`enable302=true` 模式下，STRM 内容为带 query 参数的 URL：

```
/api/strm?account=小号&pickcode=abc123def456GHI78&file_name=电影.mkv
```

路由收到请求后首先校验 `pickcode` 是否为 17 位字母数字，不合法直接 400。

### 规则 1：URL 参数（仅私网）

`?mode=redirect` 或 `?mode=proxy` 强制指定模式。

> **安全限制**：仅在私网 IP（10.x / 172.16-31.x / 192.168.x）生效，防止公网绕过 force-proxy 保护。

### 规则 2：force-proxy UA

以下客户端强制代理（seek 兼容性差，302 会导致拖动进度条卡死）：

| UA 关键词 | 客户端 |
|-----------|--------|
| `Infuse` | Infuse (iOS/macOS/tvOS) |
| `VidHub` | VidHub |
| `SenPlayer` | SenPlayer / SenPlayerHD |

> 参考自 emby2Alist 的 `clientSelfAlistRule`，115 直链 + 这些客户端的 Range 处理有已知问题。
> **配置化**：UA 列表可在 `settings.json → strm.forceProxyUaTokens` 自定义。

### 规则 3：默认 redirect

非 force-proxy 客户端默认走 302，部署设备零中转。

### 兜底 1：可达性预检（redirectCheck）

走 redirect 前先发 HEAD 请求（默认 5 秒超时）：
- 200/206 → 返回 302
- 不可达/超时 → 静默降级 proxy，用户无感

**可达性缓存**：LRU 256 条 / 4 分钟 TTL，只缓存成功结果（失败的实时探测）。

> **配置化**：超时可在 `settings.json → strm.redirectCheckTimeoutMs` 自定义。

### 兜底 2：并发限流

单账号同时 proxy 的请求数 ≥ 上限（默认 8）时，新请求切 redirect。

> 115 单账号约 10 进程上限（emby2Alist 实战经验），留 2 个余量给其他客户端。超限后所有客户端无法播放，切 redirect 比直接 502 强。
> **配置化**：上限可在 `settings.json → strm.accountProxyConcurrencyLimit` 自定义。

## 场景决策示例

| 场景 | 决策 | 原因 |
|------|------|------|
| Emby Server 播放 | redirect | Emby 跟随 302，省中转 |
| Kodi 播放 | redirect | Kodi 302 兼容性好 |
| 浏览器播放 | redirect | 浏览器原生支持 302 |
| Infuse 播放 | proxy | seek 兼容性差，强制代理 |
| 公网用 `?mode=proxy` | redirect | 公网忽略 mode 参数 |
| 内网用 `?mode=proxy` | proxy | 私网允许强制指定 |
| 115 CDN 不可达 | proxy | redirectCheck 降级 |
| 8 路并发代理 | redirect | 超限切换 |

## 缓存设计

### urlCache（直链解析缓存）

- 容量：512 条
- TTL：5 分钟
- Key：`${accountName}:${pickcode}`（不含 UA，115 直链不依赖 UA）
- Value：完整 `DownloadUrlMeta`（url + fileSize + fileName）

### reachableCache（可达性缓存）

- 容量：256 条
- TTL：4 分钟
- 只缓存成功结果（失败实时探测，避免误判）

## 真实文件大小

调用 115 download API 时解析 `file_size` 字段，用于：
- 决策日志标注 `real`（真实大小）vs `est`（文件名估算）

## 日志示例

```
[STRM] account=我的115 pickcode=xxxx…xxxx decision=redirect reason=default_redirect
[STRM] account=我的115 pickcode=xxxx…xxxx decision=proxy reason=force_proxy_ua:Infuse
[STRM] account=我的115 pickcode=xxxx…xxxx decision=redirect reason=proxy_concurrency_limit(8/8) fallback_redirect
[STRM] account=我的115 pickcode=xxxx…xxxx decision=proxy reason=redirect_check_failed(502) fallback_proxy
```

## 代理（proxy）实现细节

- 流式传输（`Content-Type`/`Content-Length`/`Content-Range` 透传）
- 逐字节回传，支持 Range 请求
- 移除 hop-by-hop 头（`Connection`/`Keep-Alive`/`Transfer-Encoding` 等）
- try/finally 保证并发计数器释放

## 可配置常量

以下常量已从硬编码迁移至 `settings.json` 配置化：

| 配置路径 | 默认值 | 说明 |
|----------|--------|------|
| `strm.forceProxyUaTokens` | `["Infuse","VidHub","SenPlayer","SenPlayerHD"]` | 强制代理 UA 列表 |
| `strm.accountProxyConcurrencyLimit` | `8` | 单账号并发代理上限 |
| `strm.redirectCheckTimeoutMs` | `5000` | HEAD 预检超时(ms) |

> 缓存策略常量（`URL_CACHE_TTL`/`URL_CACHE_SIZE`/`REACHABLE_CACHE_TTL`/`REACHABLE_CACHE_SIZE`）保持内部常量，不暴露给用户。

## STRM 内容格式

### 302 模式（enable302=true）

```
http://192.168.50.250:8090/api/strm?account=小号&pickcode=abc123def456GHI78&file_name=电影.mkv
```

- `account`：账号名
- `pickcode`：115 文件唯一标识（17 位字母数字）
- `file_name`：文件名（用于 Content-Disposition）

### 非 302 模式（enable302=false）

```
http://OpenList地址/电影/小王子.mkv
```

播放器直连 OpenList，faststrm 不参与路由决策。

### pickcode 获取方式

| 来源 | 说明 |
|------|------|
| **生活事件生成** | 115 生活事件 API 直接返回 `pick_code`，100% 可靠 |
| **全量扫描生成** | 从 filePathDb 反查 `getFilePathEntryByPath`，需路径匹配 |

> ⚠️ 全量扫描生成 STRM 时，`pickcode` 依赖 `filePathDb` 中是否已有记录。首次全量扫描后会写回 DB，后续扫描可正常反查。若 `pickcode` 缺失，`generateStrmContent` 会跳过生成并输出警告日志。
