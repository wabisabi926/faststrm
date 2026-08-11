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
请求到达
  ↓
规则1: ?mode=xxx（仅私网生效）→ 用户指定
  ↓
规则2: UA 匹配 force-proxy 列表？→ proxy
  ↓
规则3: 默认 → redirect
  ↓
兜底1: redirect 但 115 CDN 不可达（HEAD 预检失败）→ 降级 proxy
兜底2: proxy 但单账号并发 ≥ 8 → 切 redirect
```

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

### 规则 3：默认 redirect

非 force-proxy 客户端默认走 302，部署设备零中转。

### 兜底 1：可达性预检（redirectCheck）

走 redirect 前先发 HEAD 请求（5 秒超时）：
- 200/206 → 返回 302
- 不可达/超时 → 静默降级 proxy，用户无感

**可达性缓存**：LRU 256 条 / 4 分钟 TTL，只缓存成功结果（失败的实时探测）。

### 兜底 2：并发限流

单账号同时 proxy 的请求数 ≥ 8 时，新请求切 redirect。

> 115 单账号约 10 进程上限（emby2Alist 实战经验），留 2 个余量给其他客户端。超限后所有客户端无法播放，切 redirect 比直接 502 强。

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
- 大文件判断（历史功能，默认 redirect 后该规则已移除）
- 决策日志标注 `real`（真实大小）vs `est`（文件名估算）

## 日志示例

```
[STRM] account=我的115 pickcode=xxxx…xxxx decision=redirect reason=default_redirect
[STRM] account=我的115 pickcode=xxxx…xxxx decision=proxy reason=force_proxy_ua:Infuse
[STRM] account=我的115 pickcode=xxxx…xxxx decision=redirect reason=concurrency_limit (8/8)
[STRM] account=我的115 pickcode=xxxx…xxxx decision=proxy reason=redirect_check_failed
```

## 代理（proxy）实现细节

- 流式传输（`Content-Type`/`Content-Length`/`Content-Range` 透传）
- 逐字节回传，支持 Range 请求
- 移除 hop-by-hop 头（`Connection`/`Keep-Alive`/`Transfer-Encoding` 等）
- try/finally 保证并发计数器释放

## 相关配置

当前路由策略的硬编码常量（位于 `route.ts`）：

| 常量 | 值 | 说明 |
|------|-----|------|
| `FORCE_PROXY_UA_TOKENS` | `["Infuse", "VidHub", "SenPlayer", "SenPlayerHD"]` | 强制代理 UA |
| `ACCOUNT_PROXY_CONCURRENCY_LIMIT` | `8` | 单账号并发代理上限 |
| `URL_CACHE_SIZE` | `512` | urlCache 容量 |
| `URL_CACHE_TTL_MS` | `5 * 60 * 1000` | urlCache TTL |
| `REACHABLE_CACHE_SIZE` | `256` | reachableCache 容量 |
| `REACHABLE_CACHE_TTL_MS` | `4 * 60 * 1000` | reachableCache TTL |
| `REDIRECT_CHECK_TIMEOUT_MS` | `5000` | HEAD 预检超时 |

> 这些常量目前硬编码，未来可能移到 AppSettings 配置化。
