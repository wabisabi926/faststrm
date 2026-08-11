# STRM 路由策略

当 `enable302: true` 时，播放器请求 faststrm 的 `/api/strm` 接口，faststrm 根据**规则引擎**自动决策走 302 重定向还是代理中转。

---

## 核心思路

不在「纯 302」和「纯代理」二选一，而是按**客户端 / 网络 / 文件大小**自动选择，302 走不通就**静默降级代理**，保证：

- 浏览器直接打开 STRM 不报错
- 大文件不吃服务器上行
- Infuse 稳定拖动进度条

---

## 两种模式对比

### redirect（302 直连）

```
播放器 → faststrm → 302 + Location: 115 CDN URL → 播放器直连 115 CDN
```

- faststrm **不中转流量**，部署设备零带宽消耗
- 依赖播放器正确处理 302 + Range
- 适用于 Emby Server / Kodi / 浏览器等兼容性好的客户端

### proxy（代理中转）

```
播放器 → faststrm → fetch(115 CDN) → 流式回传播放器
```

- faststrm **中转流量**，占用部署设备上行
- 服务器端可控（可做超时、重试、直链刷新）
- 适用于 Infuse/VidHub/SenPlayer 等 seek 兼容性差的客户端

---

## 规则引擎（决策优先级）

```
请求到达
  ↓
规则 1: ?mode=xxx（仅私网生效）→ 用户指定
  ↓
规则 2: UA 匹配 force-proxy 列表？→ proxy
  ↓
规则 3: 客户端 IP 在局域网内？→ proxy
  ↓
规则 4: 文件名命中 ≥20GB 大小标记？→ redirect + 预检
  ↓
规则 5: 其他所有情况 → redirect（默认）
  ↓
兜底 1: redirect 但 115 CDN 不可达（HEAD 预检失败）→ 降级 proxy
兜底 2: proxy 但单账号并发 ≥ 8 → 切 redirect
```

### 规则详解

| # | 条件 | 决策 | 说明 |
|---|------|------|------|
| ① | `?mode=proxy` 或 `?mode=redirect`（仅私网） | 按参数执行 | 调试用，优先级最高 |
| ② | UA 命中 `Infuse` / `VidHub` / `SenPlayer` | **强制代理** | 这些客户端 302 + Range 配合会导致拖动进度条失败 |
| ③ | 客户端 IP 属于局域网（`192.168.*` / `10.*` / `172.16-31.*`） | **强制代理** | 家里上行够用，稳定性优先 |
| ④ | `file_name` 命中 ≥20GB 大小标记（如 `.20GB.` `.45.3G.` `.12000MB.`） | **redirect + 预检** | 大文件优先省服务器上行 |
| ⑤ | 其他所有情况 | **默认 redirect** | 开箱即用，零中转 |

---

## 302 预检（redirectCheck）

当决策命中 redirect 时，后端不会直接把 115 CDN URL 甩给客户端，而是先自己做一次**本地 HEAD 校验**：

1. 以相同 UA + Cookie + Referer + Origin 发起 `HEAD <cdnUrl>`
2. 超时 5 秒
3. HTTP 2xx/3xx → 真正返回 `302 Location: <cdnUrl>` 给客户端
4. 不可达（403 / 超时 / 网络错误）→ **静默降级到代理模式**

> 💡 这一步解决了「浏览器直接打开 115 CDN 链接被防盗链拦 → 无法访问此页面」的根因。

### 可达性缓存

| 属性 | 值 |
|------|-----|
| 容量 | 256 条 |
| TTL | 4 分钟 |
| 策略 | 只缓存成功结果，失败实时探测 |

---

## 并发限流

单账号同时 proxy 的请求数 ≥ 8 时，新请求自动切 redirect：

> 115 单账号约 10 进程上限，留 2 个余量给其他客户端。超限后切 redirect 比直接 502 强。

---

## 缓存设计

### urlCache（直链解析缓存）

| 属性 | 值 |
|------|-----|
| 容量 | 512 条 |
| TTL | 5 分钟 |
| Key | `{accountName}:{pickcode}`（不含 UA） |
| 用途 | 缓存 `getDownloadUrlWeb()` 结果，避免重复请求 115 接口 |

### reachableCache（可达性缓存）

| 属性 | 值 |
|------|-----|
| 容量 | 256 条 |
| TTL | 4 分钟 |
| 策略 | 只缓存成功结果（失败实时探测） |

---

## 场景决策示例

| 场景 | 决策 | 原因 |
|------|------|------|
| Emby Server 播放 | redirect | Emby 跟随 302，省中转 |
| Kodi 播放 | redirect | Kodi 302 兼容性好 |
| 浏览器播放 | redirect | 浏览器原生支持 302 |
| Infuse 播放 | proxy | seek 兼容性差，强制代理 |
| 公网用 `?mode=proxy` | redirect | 公网忽略 mode 参数 |
| 内网用 `?mode=proxy` | proxy | 私网允许强制指定 |
| 115 CDN 不可达 | proxy | redirectCheck 静默降级 |
| 8 路并发代理 | redirect | 超限自动切换 |
| 45GB 蓝光原盘 | redirect | 大文件优先省上行 |

---

## 排障日志格式

```
[STRM] account=我的115 pickcode=cscm…mhv decision=proxy  reason=private_network_prefers_proxy           redirect_check=skipped elapsed=112ms
[STRM] account=我的115 pickcode=abcd…xyz decision=redirect reason=large_file_ge_20GB(25GB)             redirect_check=200     elapsed=214ms
[STRM] account=我的115 pickcode=wxyz…999 decision=proxy  reason=large_file_ge_20GB(22GB) -> redirect_check_failed(403) fallback_proxy  redirect_check=403 elapsed=780ms
[STRM] account=我的115 pickcode=xxxx…xxx decision=proxy  reason=force_proxy_ua:Infuse               redirect_check=skipped elapsed=97ms
```

---

## 相关配置常量

以下常量硬编码于 `frontend/src/app/api/strm/route.ts`，未来可能移至配置化：

| 常量 | 值 | 说明 |
|------|-----|------|
| `FORCE_PROXY_UA_TOKENS` | `["Infuse", "VidHub", "SenPlayer"]` | 强制代理 UA |
| `ACCOUNT_PROXY_CONCURRENCY_LIMIT` | `8` | 单账号并发代理上限 |
| `URL_CACHE_SIZE` | `512` | 直链解析缓存容量 |
| `URL_CACHE_TTL_MS` | `300000` (5分钟) | 直链解析缓存 TTL |
| `REACHABLE_CACHE_SIZE` | `256` | 可达性缓存容量 |
| `REACHABLE_CACHE_TTL_MS` | `240000` (4分钟) | 可达性缓存 TTL |
| `REDIRECT_CHECK_TIMEOUT_MS` | `5000` | HEAD 预检超时 |
