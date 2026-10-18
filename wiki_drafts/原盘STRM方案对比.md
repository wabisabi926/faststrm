# 原盘 STRM 方案对比与补充建议

> 分析对象：faststrm v1.4.2、p115strmhelper v2.8.79、LitePan
> 结论：faststrm 的原盘处理**已经是三者中最完整的**，但有 3 处可以补，其中 1 处是文档与代码漂移

---

## 1. STRM 的三要素（原盘场景下都关键）

| 要素 | 作用 | 原盘特殊之处 |
|---|---|---|
| **文件名** | Emby 靠扩展名推断容器类型 | `.iso` 必须用**双扩展名** `xxx.iso.strm` |
| **内容 URL** | 指向本服务的 `/api/strm` | 原盘与大文件都需要 Range |
| **`file_name` 参数** | Content-Disposition + 格式判定 | 必须保留 `.iso`，否则容器判定丢失 |

### 为什么必须 `.iso.strm`

Emby 扫描时看到 `.strm` 会读取内容作为媒体源，但**容器类型是从 STRM 文件名的扩展名推断的**：

```
星际穿越.iso          → Emby 当作 ISO 原盘（支持菜单/章节/多音轨）
星际穿越.strm         → Emby 当作普通视频，容器未知 → 播放时按转码处理
星际穿越.iso.strm     → Emby 识别为 ISO 容器 ✅
```

三个项目在这一点的实现完全一致：

- faststrm：`getStrmFileName`（`task/executor_utils.go:139-149`）对 `.iso` 返回 `{stem}.iso.strm`
- p115strmhelper：`utils/strm.py:681-682` 同样 `f"{stem}.iso.strm"`
- LitePan：无特殊处理（它的 STRM 里不带文件名语义，靠 URL 路径）

---

## 2. 原盘的两种形态

| 形态 | 目录结构 | 能否生成有效 STRM |
|---|---|---|
| **ISO 单文件** | `电影/星际穿越.iso` | ✅ 可以，双扩展名 |
| **BDMV 目录** | `电影/星际穿越/BDMV/STREAM/00000.m2ts` + `CERTIFICATE/` | ⚠️ 没有单一入口文件 |

BDMV 目录的问题在于：一部片子被拆成上百个 `.m2ts`，其中只有 1 个是主视频流，其余是花絮/片段。如果无差别生成 STRM，媒体库里会多出上百个"假剧集"。

---

## 3. 三个项目的处理对比

| 维度 | faststrm | p115strmhelper | LitePan |
|---|---|---|---|
| ISO 双扩展名 | ✅ | ✅ | ❌ |
| BDMV/STREAM 跳过 | ✅ 硬编码跳过整棵子树 | ✅ 可开关（`skip_bdmv_stream`，默认开） | ❌ **无跳过**，靠用户配 `excludeDirs` 关键词 |
| 原盘目录整体处理 | 跳过 → 该片无 STRM | 跳过 → 明确提示"蓝光原盘不支持生成 STRM" | 会为每个 m2ts 生成 STRM |
| 播放方式 | **302 / 代理流双路径** | 只有 302 | 302 / 代理流双路径 |
| 经 Emby 播放 | ✅ 反代强制 DirectPlay + 代理流 | ❌ 无反代 | ✅ 反代 + Range 代理 |
| 容器判定 | 扩展名 + Emby Container 双判据 | 仅 URL | — |

**关键差异**：LitePan 的默认扩展名里含 `m2ts` 且没有 BDMV 跳过逻辑，扫到一个 BDMV 原盘会生成上百个 STRM。这是 faststrm 明显做得更好的地方，不该退步。

---

## 4. 播放链路（faststrm 现状）

```
【ISO 直连客户端（Infuse/Kodi 直接读 strm）】
客户端 → /api/strm?pickcode= → DecideRoute 默认 redirect → 302 CDN
        → 客户端自己对 CDN 发 Range（Infuse/Kodi 支持 ISO over HTTP）

【ISO 经 Emby 播放】
客户端 → EmbyProxy(8097) → PlaybackInfo 改写（强制 DirectPlay）
        → /videos/{id}/stream
        → isSeekRequiredFormat 命中 iso/bdmv/m2ts/ts
        → proxyStreamToStrm（服务端代理，透传 Range）
        → /api/strm（proxy 模式）→ 115 CDN
```

两层策略是**故意不同**的：

- STRM 层默认 302（`router.go:409-411` 注释：原盘同样走 302/原生 Range）
- EmbyProxy 层强制代理（`proxy.go:1208-1215` 注释：302 后客户端 Range 直连 CDN 可能因 UA 绑定/签名过期被拒）

这个设计是对的：直连客户端自己处理 ISO，经 Emby 时由服务端保证 Range 稳定。

---

## 5. 三处可补充（按价值排序）

### 补充 1：文档与代码漂移（建议立即修）

`wiki_drafts/STRM-路由策略.md:156` 和 `Emby集成.md:71` 都写着「ISO 自动 proxy」，日志示例也是 `decision=proxy reason=format_force_proxy:iso`。

但代码里 **STRM 层已经不再对 ISO 特判**：

```go
// internal/service/strm/router.go:409-411
// P3-ISO：原盘同样走 302/原生 Range，不经过自研 proxy
return DecisionResult{Decision: DecisionRedirect, Reason: "default_redirect"}
```

用户照着文档排查会得出错误结论。**建议改文档，不改代码**（当前代码行为更优：直连客户端可省服务器带宽）。

### 补充 2：`.ts` 被过度列入强制代理格式（✅ 已做，改为可配置）

```go
// 改前（internal/service/embyproxy/proxy.go）
var seekRequiredExts = map[string]bool{
	".iso": true, ".bdmv": true, ".bdav": true,
	".ts": true, ".m2ts": true, ".vob": true, ".ifo": true, ".bup": true,
}
```

`.ts` 是常见的**录播/直播录制**格式，不是原盘。命中后所有 TS 文件都走服务端代理，白白消耗服务器带宽（TS 文件动辄几十 GB）。

**已实现**：列表提取为 `settings.strm.forceProxyFormats`，默认值与原硬编码完全一致（不改变现有行为）。
用户把 `ts` 从列表里删掉后，TS 文件即走 302 直连 CDN。

```json
"strm": {
  "forceProxyFormats": ["iso", "bdmv", "bdav", "dvd", "bluray", "m2ts", "vob", "ifo", "bup"]
}
```

匹配规则：大小写不敏感、点号可省略（`"iso"` 与 `".iso"` 等价），同时匹配 Emby 的 Container 字段与文件名扩展名。
配置为空时回落内置默认值；保存 Emby 设置时热生效，无需重启。

### 补充 3：BDMV 目录可选生成"主视频流"STRM（需确认）

现状是**整个原盘目录零 STRM**，这部片在 Emby 里完全不可见。这是三家共同的做法，但用户常常抱怨"原盘扫不出来"。

可选方案：新增 `bdmvMode: skip(默认) | main_stream`。开启后扫描时若发现 `BDMV/STREAM`，取其中**体积最大的视频文件**，在电影目录层级生成 `{目录名}.strm` 指向它的 pickcode。

- 优点：原盘能在 Emby 里看到并直接播放主视频（多音轨仍在）
- 缺点：丢失原盘菜单/章节结构；需要用户显式开启
- 实现成本：中等（扫描阶段需要「先探到 BDMV 再回溯父目录」的逻辑）

**这条会改变扫描行为，建议单独确认后再做。**

---

## 6. 明确不建议做的

| 不建议 | 原因 |
|---|---|
| 对 BDMV 目录无差别生成 m2ts STRM（LitePan 的做法） | 一个原盘变上百个条目，媒体库直接污染 |
| 移除 `.iso.strm` 双扩展名 | Emby 会丢失 ISO 容器判定，播放退化 |
| STRM 层对 ISO 强制代理 | 直连客户端（Infuse/Kodi）会白白多一跳，服务器带宽翻倍 |
| 自动把 BDMV 转成 ISO | 需要服务端重组文件，115 侧无此能力，超出 STRM 工具边界 |

---

## 7. 结论

**faststrm 的原盘能力已经是三者最强**：双扩展名 + BDMV 跳过 + 反代强制 DirectPlay + 代理流 Range，这套组合 p115strmhelper 和 LitePan 都没有完整实现。

本轮建议只做**补充 1（文档修正）**，补充 2/3 等确认后再排期。

---

## 8. 关于补充 3（BDMV 主视频流）的评估结论：暂不做

| 维度 | 评估 |
|---|---|
| 真实收益 | 中。让 BDMV 原盘在 Emby 里"看得见"，但播的是无菜单的单条 m2ts |
| 实现成本 | 中高。当前是单遍 DFS，遇到 `BDMV/STREAM` 直接 `continue`；要生成父目录级 STRM，必须先探到 STREAM 再回溯父目录，等于把扫描改成两阶段 |
| 误判风险 | 中。"体积最大的 m2ts"未必是主片（多版本原盘、演唱会花絮、导演剪辑版并存时会串） |
| 一致性风险 | 高。生成 `{目录名}.strm` 后 Emby 会把它当电影刮削，若刮削源匹配到花絮，会污染媒体库 |
| 三家现状 | 都不做（faststrm 跳过、p115strmhelper 明确提示"不支持"、LitePan 会生成上百个 m2ts） |

**结论：不做自动生成。** 现有"跳过"行为是经过权衡的正确选择，不该为了"看得见"引入误判。

### 替代方案（成本极低，建议做）

现状真正的问题不是"没生成"，而是**用户不知道为什么原盘不见了**。

`listAllFilesRecursive` 其实已经统计了 `bdmvSkipped` 计数并打了日志
（`跳过 N 个 BDMV/STREAM 原盘视频流（不生成 STRM）`），但这个信息：
- 只在服务端日志里，Web UI 的任务详情看不到
- 没有进任务结果/通知

建议后续把它提升为**任务结果里的显式提示**（例如"本次跳过 3 个 BDMV 原盘目录"），
让用户知道"这是设计行为，不是 bug"，并指引他去转 ISO 或手动指定主视频流。

成本约 20 行，无行为风险，解决的是信息缺失而非功能缺失。
