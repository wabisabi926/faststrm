# STRM 路由策略

当 `enable302: true` 时，播放器请求 faststrm 的 `/api/strm` 接口，faststrm 根据**规则引擎**自动决策走 302 重定向还是代理中转。

---

## 核心思路

不在「纯 302」和「纯代理」二选一，而是按**客户端 / 网络 / pickcode** 自动选择，302 走不通就**静默降级代理**，保证：

- 浏览器直接打开 STRM 不报错
- 大文件不吃服务器上行
- Infuse 稳定拖动进度条
- pickcode 缺失时安全跳过，不生成无效 STRM

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
请求到达 /api/strm?account=xxx&pickcode=xxx&file_name=xxx
  ↓
规则0: 校验 pickcode 格式（17 位字母数字）→ 400 非法
  ↓
规则1: ?mode=xxx（仅私网生效）→ 用户指定
  ↓
规则2: UA 匹配 force-proxy 列表？→ 强制 proxy
  ↓
规则3: 默认 → redirect（302 直连）
  ↓
兜底1: redirect 但 HEAD 预检失败（CDN 不可达）→ 降级 proxy
兜底2: proxy 但单账号并发 ≥ 上限 → 切 redirect
```

### 规则详解

| # | 条件 | 决策 | 说明 |
|---|------|------|------|
| 0 | `pickcode` 非 17 位字母数字 | **400 拒绝** | 非法 pickcode 直接报错，不往下走 |
| ① | `?mode=proxy` 或 `?mode=redirect`（仅私网） | 按参数执行 | 调试用，优先级最高 |
| ② | UA 命中 `forceProxyUaTokens` 列表 | **强制代理** | 这些客户端 302 + Range 配合会导致拖动进度条失败 |
| ③ | 其他所有情况 | **默认 redirect** | 开箱即用，零中转 |

> **配置化**：规则 ② 的 UA 列表可在 `settings.json → strm.forceProxyUaTokens` 自定义，默认 `["Infuse","VidHub","SenPlayer","SenPlayerHD"]`。

---

## 302 预检（redirectCheck）

当决策命中 redirect 时，后端不会直接把 115 CDN URL 甩给客户端，而是先自己做一次**本地 HEAD 校验**：

1. 以相同 UA + Cookie + Referer + Origin 发起 `HEAD <cdnUrl>`
2. 超时 5 秒（可配置）
3. HTTP 2xx/3xx → 真正返回 `302 Location: <cdnUrl>` 给客户端
4. 不可达（403 / 超时 / 网络错误）→ **静默降级到代理模式**

> 💡 这一步解决了「浏览器直接打开 115 CDN 链接被防盗链拦 → 无法访问此页面」的根因。

### 可达性缓存

| 属性 | 值 |
|------|-----|
| 容量 | 256 条 |
| TTL | 4 分钟 |
| 策略 | 只缓存成功结果，失败实时探测 |

> **配置化**：超时可在 `settings.json → strm.redirectCheckTimeoutMs` 自定义，默认 5000ms。

---

## 并发限流

单账号同时 proxy 的请求数 ≥ 上限（默认 8）时，新请求自动切 redirect：

> 115 单账号约 10 进程上限，留 2 个余量给其他客户端。超限后切 redirect 比直接 502 强。
> **配置化**：上限可在 `settings.json → strm.accountProxyConcurrencyLimit` 自定义。

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

---

## STRM 内容格式

### 302 模式（enable302=true）

```
http://192.168.50.250:3000/api/strm?account=小号&pickcode=abc123def456GHI78&file_name=电影.mkv
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

---

## pickcode 缺失兜底保护

当 `enable302=true` 但 `pickcode` 缺失时，系统有 4 层防御链：

1. `generateStrmContent` 返回空字符串 + `console.warn` 警告
2. `enqueueForAccount.downloadOrCreateStrm` 检查空字符串，跳过写入
3. `strmCleanup` 补生成时检查空字符串，跳过并记录错误
4. `syncStrmText` 防御性检查，拒绝写入空内容

日志示例：
```
[STRM] enable302=true 但 pickcode 缺失，跳过生成: cloudPath=电影/xxx.mkv, account=小号
```

---

## 排障日志格式

```
[STRM] account=我的115 pickcode=cscm…mhv decision=proxy  reason=force_proxy_ua:Infuse               redirect_check=skipped elapsed=112ms
[STRM] account=我的115 pickcode=abcd…xyz decision=redirect reason=default_redirect                   redirect_check=200     elapsed=214ms
[STRM] account=我的115 pickcode=wxyz…999 decision=proxy  reason=default_redirect -> redirect_check_failed(403) fallback_proxy  redirect_check=403 elapsed=780ms
[STRM] account=我的115 pickcode=xxxx…xxx decision=redirect reason=proxy_concurrency_limit(8/8) fallback_redirect  redirect_check=skipped elapsed=97ms
```

---

## 配置化常量

以下常量已从硬编码迁移至 `settings.json` 配置化：

| 配置路径 | 默认值 | 说明 |
|----------|--------|------|
| `strm.forceProxyUaTokens` | `["Infuse","VidHub","SenPlayer","SenPlayerHD"]` | 强制代理 UA 列表 |
| `strm.accountProxyConcurrencyLimit` | `8` | 单账号并发代理上限 |
| `strm.redirectCheckTimeoutMs` | `5000` | HEAD 预检超时(ms) |

> 缓存策略常量（`URL_CACHE_TTL`/`URL_CACHE_SIZE`/`REACHABLE_CACHE_TTL`/`REACHABLE_CACHE_SIZE`）保持内部常量，不暴露给用户。
> 所有配置修改后实时生效，无需重启容器。
