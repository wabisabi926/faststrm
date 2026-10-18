# p115strmhelper 单点对标与"减法"方案

> 2026-10-18 · 只对比 p115strmhelper（不再参考 LitePan）。
> 前提：M1/M2/M3/M6 与"写入去重 + 目录级失败容忍"已上线（HEAD `ec7d98d`），M5 未开工。
> 本文的目标不是"再加功能"，而是**定清楚哪些做、哪些删**。

---

## 0. 一句话结论

| 领域 | 结论 |
|---|---|
| 全量生成 | M5（`cur=0`）值得做，但必须先按 `优化方案-详细实施版.md` 17.5 节修正 6 处；同时删掉 `ExportDirParse` 这段"每任务白等 30 秒、还在 115 建一个 zip 任务"的死代码 |
| 增量生成 | **建议整体删除**。faststrm 的"增量"并不省扫描，只省写盘；写入去重已覆盖；它还是全仓唯一一条"清单不全就删库"的清理路径 |
| 生活事件 | 功能不缺，缺 3 个正确性细节（整理完成等待 / 405 降级 / 关联资产迁移覆盖），**不扩张代码** |
| ISO / 原盘 | 已经是三家最强，**不再加功能** |
| 代码量 | 本轮净减约 **400 行**（不含前端），没有新增抽象 |

---

## 1. 对标总表

| 能力 | p115strmhelper | faststrm 现状 | 结论 |
|---|---|---|---|
| 清单获取 | `iter_files(cur=0, with_path, with_ancestors, cooldown=1.5)` | 逐目录 DFS，1 次调用/目录 | 改（M5） |
| 路径还原 | p115client 内部 `ensure_path`（pid→path 缓存） | `folders` 表 cid→path + `fsAncestorsViaFiles` 祖先链 | 已有，M5 复用 |
| 写入 | 8 个 IO 线程 + 批量 flush | WorkerPool + 原子写 + **内容比对跳过** | faststrm 更优，保留 |
| 下载关联文件 | `auto_download_mediainfo`（nfo/海报，白/黑名单） | `AutoDownloadMetadata` + `DownloadExtensions`（已有） | 保留 |
| 清理 | `remove_unless_strm` 单机制（树差集）+ 二次确认（界面/Telegram） | **两套并存**：路径级 `removeExtraFiles`（**默认开**）+ pickcode 级 `cleanupOrphanStrms`（**默认关**） | 默认值反了，见 6.4 |
| 增量 | `export_dir` 全树导出 + 本地树扫描 → 差集 | files 表快照比对（**仍全量扫云端**） | **删** |
| 生活事件 | create/move/rename/remove + 整理完成事件 + 405 降级 + 资产迁移 + 通知合并 | create/move/rename/delete + DB 反查 + 去重 + 通知 + Emby 刷新 | 补 3 个细节 |
| ISO | `.iso.strm` 双扩展名 | 双扩展名 + 反代强制 DirectPlay + Range | 保留，不加 |
| 蓝光原盘 | `is_bluray_folder` → 不生成 | 跳过 `BDMV/STREAM` + 计数提示 | 保留 |
| Rust 核心 | 可选（`full_sync_process_rust`） | 无 | 不做 |
| 多路径配置 | `本地#云端#启用` 一行一条 | 任务实体 | 不做 |

---

## 2. 全量生成

### 2.1 M5 六步（修正版，详见 17.5）

| # | 修正点 | 不修的后果 |
|---|---|---|
| 1 | 清理是 **pickcode 维度**：翻译失败的条目没进 `fileEntries` → 本地 STRM 被当孤儿删 | 误删 |
| 2 | 分页终止用**空页**（250ms 重试 1 次），`count` 只做结束校验 | 漏扫/死循环 |
| 3 | `FsFilesAll` 独立 60–90s 超时（`Client.HTTP.Timeout=15s` 不够） | 大库直接退化 DFS |
| 4 | 新增 `FullListEntry` + 字段别名（`fid/file_id`、`fn/file_name`、`pc/pick_code`、`size/size_byte`） | 整批解析为空 |
| 5 | pid→路径复用 `fsAncestorsViaFiles`（`FsFiles(pid,limit=1)` 的 `path`），三级回退 + 回填 folders | 冷启动无法定位 |
| 6 | 子树连 DFS 都失败 → **整任务禁清理**（不用比例阈值）；folders 表做 parent_id 一致性校验 | 误删 / 路径写错 |

预期收益：热启动 10–50×；冷启动 ≈ DFS 持平（跑一次即转热）。发布说明必须写清楚。

### 2.2 删除 `ExportDirParse`

- `executor.go:272-289`：每任务调用一次，`context.WithTimeout(..., 30s)`，**结果只用于打日志**（`rootPick` 之后没有任何使用）。
- `client115/files.go:533-637`（105 行）：提交 115 目录导出任务 + 轮询。副作用是**每跑一次任务就在用户 115 账号里建一个 zip 导出任务**。
- 结论：删除调用与实现，任务启动少一次最长 30s 的阻塞、少一个 115 侧垃圾任务。

### 2.3 下载幂等（替代增量的关键）

`runDownloads`（`executor_utils.go:633`）目前无条件下载。补一条：

```
本地文件存在 && 大小 == 云端大小 && mtime 不早于上次成功 → 跳过（计入 skipped）
```

约 10 行。这是删掉"增量模式"的前提，否则每轮任务都会重下全部 nfo/字幕。

### 2.4 顺手清理

- 逐条目 `Info` 日志（`executor_utils.go:500`）降 `Debug`：10 万文件 = 10 万行日志。
- 进度从"已扫 N 目录"改为 `N/count`（M5 下 `count` 已知，可给真实百分比）。

---

## 3. 增量生成：建议整体删除

### 3.1 事实

```go
// executor.go:311  先全量扫云端
fileEntries, err := listAllFilesRecursive(...)
// executor.go:355  再比对快照
incremental := settings.Download.IncrementalSync && ...
```

- 扫描一点没省，省的是：写盘、Emby 刷新、下载。
- 写盘与 Emby 刷新已被"写入去重"（`3587093`）覆盖。
- 下载是唯一剩下的收益 → 用 2.3 的 10 行替代。
- 增量清理 `cloudPickcodes = buildCloudPickcodeSet(fileEntries)`（`executor.go:432`）**不并 DB 快照**，是全仓唯一"清单不全就删库"的路径。

### 3.2 p115 的增量是另一回事，明确不抄

它用 `export_dir`（115 导出目录树）拉全树 + 扫本地树 → 差集，只处理新增。依赖：导出任务、文件锁、路径转义（`custom_escape` 处理 `\'`）、树缓存。复杂度高，收益与 M5 重叠。

### 3.3 删除清单

| 项 | 位置 | 行数 |
|---|---|---|
| 增量分支（快照比对 / skip 标记 / 增量清理） | `task/executor.go:355-447` | ~93 |
| `IncrementalSync` / `IncrementalCleanupMode` 配置 | `model/settings.go:120,127`、`handler/emby.go:489` | ~10 |
| 快照查询 `ListSnapshotByAccount` | `db/filepath_repo.go:255` | **保留**（全量清理 `executor.go:470` 仍用它做 DB 快照兜底） |
| 前端开关 | `BasicSettingsTab.tsx:395-412`、`types.ts:59` | ~20 |

迁移：老配置里 `incrementalSync=true` 直接忽略（字段保留解析、不生效），避免用户配置报错。

---

## 4. 生活事件监控

### 4.1 保留（不扩张）

create / rename / move / delete、DB 反查路径、事件去重（`dedup.go`）、通知合并、Emby 刷新、关联资产重命名。这部分 faststrm 与 p115 等价，代码量已不小（monitor 包 7638 行），**不再加流程**。

### 4.2 补 3 个正确性细节

| # | 问题 | p115 做法 | 建议（faststrm 版） |
|---|---|---|---|
| a | 115 整理（transfer）事件早于文件落盘，create 时取直链 404/403 → STRM 指向死链 | `_wait_for_transfer_complete` 阻塞等待 | **不引入阻塞队列**：create 取直链失败时退避重试 2 次（5s/15s），仍失败则记 warn 并留给下一轮全量 |
| b | iOS 接口 405（风控）后整批事件拉取失败 | `_record_ios_405` → 切 web fallback + 冷却 | 已有 proapi/ios ↔ webapi 逐页轮换（`life.go:334`），补显式 405 → 强制 webapi + 5 分钟冷却 |
| c | move/rename 时 nfo/海报/字幕是否全部跟随 | `_move_local_media_assets` + `_move_local_related_asset` | 核对现有 `moveOrRenameRelatedAssets` 是否覆盖 `.nfo/-thumb/-poster/-fanart`，缺的补进同一函数 |

### 4.3 不做

事件队列持久化、跨账号事件合并、整理目录（transfer path）全流程监控、Telegram 按钮交互。

---

## 5. ISO / 原盘

| 项 | p115strmhelper | faststrm | 结论 |
|---|---|---|---|
| `.iso.strm` 双扩展名 | 有 | 有（`getStrmFileName`） | 保留 |
| 蓝光原盘 | 整个文件夹不生成 | 跳过 `BDMV/STREAM`，其余照常 | 保留 |
| 反代播放 | 无（只有 STRM） | ISO 强制 DirectPlay + Range | 保留 |
| 强制代理格式 | 无 | 可配置（`.ts` 已可移出） | 保留 |
| BDMV 主视频流 STRM | 不做 | 不做（已评估） | 不做 |

**ISO 侧不加任何新功能**。唯一遗留项是文档已记录的"BDMV 主视频流"，结论仍是不做。

---

## 6. 代码清理清单

| # | 项 | 位置 | 删除量 | 风险 | 建议 |
|---|---|---|---|---|---|
| 1 | `ExportDirParse` + 调用 | `client115/files.go:533-637`、`task/executor.go:272-289` | ~123 行 | 零 | **做** |
| 2 | 4 个重复函数合并到 `pkg/strmutil`：`getStrmFileName` / `isValidPickcode` / `shouldGenerateStrm` / `writeStrmFile` | `task/executor_utils.go`、`monitor/event_helpers.go` | ~89 行 | 零（两份实现逐行相同） | **做** |
| 3 | 增量模式 | `task/executor.go:355-447` + 配置 + 前端 | ~93 行 | 中（需 2.3 先落地） | **做** |
| 4 | 路径级清理 `removeExtraFiles`（与 pickcode 级重复，且默认开） | `task/executor_utils.go:776-880`、`model/settings.go:337` | ~104 行 | 中（行为变化） | 先反转默认值，再删 |
| 5 | 逐条目 Info 日志 | `task/executor_utils.go:500` | 0 | 零 | **做** |

合计：净减约 **400 行**（1+2+3+5 ≈ 305 行，含 4 则 ~409 行），不新增任何抽象层。

> 关于第 4 项：默认值是反的 —— 路径级 `RemoveExtraFiles: true`（默认开，会删掉"保留集合"之外的**任何**本地文件），pickcode 级 `FullSyncCleanupOrphans: ""`（默认关，只看 STRM 内容、有 DB 快照兜底、有二次确认）。建议先把默认值反过来（一行改动，风险最低），再考虑删除路径级实现。

---

## 7. 实施顺序

| 批次 | 内容 | 风险 | 预估 |
|---|---|---|---|
| P0 | 清理 1 + 2 + 5（-212 行，纯删除/去重） | 零 | 小 |
| P1 | 全量 M5（17.5 六步） | 中 | 大 |
| P2 | 下载幂等 + 删除增量（3 + 2.3） | 中 | 中 |
| P3 | 监控 3 个细节 + 清理合并（4） | 中 | 中 |

P0 与 P1/P2 无依赖，可以并行；P2 依赖 2.3；P3 最后。

---

## 8. 明确不做

- Rust 核心、8 线程写队列（写入不是瓶颈，115 API 才是）
- `export_dir` 全树差集增量、导出树缓存
- mediainfo 自动下载（nfo/海报生成）
- Telegram 按钮式清理确认（已有延迟批次 + 确认页）
- 多路径一行配置（任务实体已覆盖）
- 清理稳定性 CV 检查、结构化进度 API
- BDMV 主视频流 STRM

---

## 9. 验收

- `go build ./...`、`go test ./...`、`gofmt -l internal/ pkg/` 全绿
- 清理批次：删除后 `grep` 无残留引用；老配置文件可正常加载
- M5：假 115 服务覆盖正常分页 / 空页 / count 变化 / 重复页 / 缺 fid / 404 回退；翻译失败子树存在本地 STRM 时清理必须被禁用
- 增量删除：老配置 `incrementalSync=true` 不报错；下载幂等命中时任务时长明显下降

---

## 10. 实施记录

### P0（已完成，2026-10-18）

| 项 | 结果 |
|---|---|
| 删除 `ExportDirParse` | 实现 104 行（`files.go`）+ 调用 17 行（`executor.go`）；任务启动不再白等最多 30s，也不再在 115 侧建 zip 导出任务 |
| 4 个重复函数合并 | 新增 `pkg/strmutil/strmfile.go`（`StrmFileName` / `IsValidPickcode` / `ShouldGenerateStrm` / `WriteStrmFile`），task 与 monitor 各自只留 1 行薄封装；调用点未动，零回归风险 |
| 日志降噪 | 逐条目 `entry name=...` 与每页 `page=...` 由 Info 降为 Debug；任务日志只保留每目录汇总 |
| 测试 | 新增 `pkg/strmutil/strmfile_test.go`（ISO 双扩展名 / pickcode / 黑名单 / 写入去重含 mtime 断言）；`go test ./...` 全绿 |

净减约 150 行（删除 283 行，新增共用实现 101 行 + 测试）。

### P1（已完成，2026-10-18）

M5 全量清单按 17.5 六步落地，详见 `优化方案-详细实施版.md` 第 20 节。与计划的唯一偏差：
**空清单不返回空结果，而是回退 DFS 复核**——避免用空集合参与清理。

### P2 / P3（待办）

- P2：下载幂等 + 删除增量（用户确认先放着）
- P3：生活事件 3 个细节 + 清理默认值反转

---

## 11. 增量生成重新评估：建议删除（含"全量之后会怎样"）

### 11.1 它到底做了什么（代码实证）

```go
// executor.go:339  注意：全量扫描在这一行之前已经跑完了
incremental := settings.Download.IncrementalSync && deps.SQLiteDB != nil && task.Account != ""
// executor.go:360-380  快照命中 → f.Kind = kindSkip
```

被标成 `kindSkip` 的条目，实际只省下三件事：

| 省下的动作 | 是否还有价值 |
|---|---|
| 写 STRM | **没有了**——写入去重（`WriteStrmFile` 内容比对）本来就会跳过 |
| Emby 刷新 | **没有了**——写入去重返回 `unchanged` 时本来就不刷 |
| 下载关联文件（srt/ass/sub/nfo/jpg/png，`DefaultDownloadExtensions`） | **有**，这是它唯一不可替代的作用 |
| 本地产物 stat（`localArtifactExists`，`executor_utils.go:129`） | 反而多了一次 stat |

清理侧（`executor.go:399-432`）：增量清理只并**本次清单**，不并 DB 快照，是全仓唯一一条"清单不全就删库"的路径。

### 11.2 删掉之后，定时任务是不是都变全量？

**是。** 而且今天其实已经是这样：增量与否由**全局开关**决定（`TaskScheduleDialog.tsx:322` 原文："执行策略（全量 / 增量）由「设置 → 增量同步」全局开关控制，定时执行与手动执行完全一致"），跟任务本身无关。删掉开关后：

- 所有任务（手动 / 定时 / 重跑）= 同一条全量路径
- "全量" ≠ "全量重写"：M5 页级清单 + 内容比对写盘 + （待补）下载幂等
- Emby 侧无副作用：内容没变就不写盘，mtime 不变，不会触发假重扫

### 11.3 优劣势

| 维度 | 保留增量 | 删除增量（全量 + 幂等） |
|---|---|---|
| 代码路径 | 2 条（含一条特殊清理） | 1 条 |
| 清理安全 | 增量清理无 DB 快照兜底 | 只剩全量清理，有快照兜底 |
| 每次运行 | 云端全扫 + 每文件 1 次 stat | 云端全扫（页级）+ 每文件 1 次小文件读 + 下载前 stat |
| 下载 | 未变化跳过 | **必须补**"本地存在且大小一致则跳过"（约 10 行） |
| 配置项 | 2 个全局项 + 1 个前端开关 | 0 |
| 用户心智 | "我开了增量，为什么还扫这么久" | 只有一种行为 |

### 11.4 代价量化

以 5 万文件、其中约 3 万个关联文件（nfo/jpg/srt）为例：

| 环节 | 增量 | 全量（M5 + 幂等） |
|---|---|---|
| 云端扫描 | 全量（页级） | 全量（页级），**相同** |
| STRM 写盘 | 5 万次 stat | 5 万次 ~120B 读（SSD 1–2s，HDD/NAS 20–40s） |
| 关联文件下载 | 0 次 | 3 万次 stat，未变化不下载 |
| 清理 | 增量清理（危险） | 全量清理（本来每轮也扫本地） |

结论：额外成本 ≈ 把 5 万次 stat 换成 5 万次小文件读。对 NAS 用户是几十秒级，可接受；换来一条代码路径 + 去掉一条无兜底的清理。

### 11.5 删除清单

| 项 | 位置 | 行数 |
|---|---|---|
| 增量分支（快照比对 / kindSkip / 修复） | `executor.go:339-397` | ~59 |
| 增量清理分支 | `executor.go:399-432` | ~34 |
| `localArtifactExists`（只为增量存在） | `executor_utils.go:126-150` | ~25 |
| `IncrementalSync` / `IncrementalCleanupMode` | `model/settings.go:120,127`、`handler/emby.go:489` | ~10 |
| 前端开关与说明 | `BasicSettingsTab.tsx:391-412`、`types.ts:58`、`TaskScheduleDialog.tsx:322` | ~25 |
| `StrmHistoryKindIncrement`（如仅增量使用） | `db/strm_history_repo.go:18` | ~2 |

**保留**：`ListSnapshotByAccount`（全量清理 `executor.go:470` 仍在用）。

---

## 12. 清理默认值：建议反转，但不是"一行翻转"

### 12.1 现状（三个事实）

1. **全局 `settings.RemoveExtraFiles` 是死配置**：只有设置接口读写（`handler/emby.go:426,474`），执行器从不读它——真正生效的是任务级 `task.RemoveExtraFiles`（`executor.go:673`）。
2. 任务级开关前端默认 **true**（`AddTaskDialog.tsx:125`），即新建任务默认会"删掉保留集合之外的任何文件"。
3. pickcode 级 `FullSyncCleanupOrphans` 默认空 = 关闭，而且**前端没有任何入口**，只能手改 `settings.json`。

也就是说：绝大多数用户实际只有**路径级清理**在跑，而它是三套清理里最激进的一套。

### 12.2 风险对比

| | 路径级 `removeExtraFiles` | pickcode 级 `cleanupOrphanStrms` |
|---|---|---|
| 删除对象 | `targetPath` 下**任何**不在保留集合的文件 | 只删 `.strm`（可选连带同名 nfo/jpg/srt） |
| 判据 | 路径 | STRM 内容里的 pickcode |
| 误删用户自己的文件 | **会**（占比 <10% 时直接删，如自建海报、非默认扩展名字幕） | 不会（非 faststrm 生成的 STRM 直接跳过） |
| DB 兜底 | 无 | 全量清理并 DB 快照 |
| 二次确认 | 有（超 StableThreshold） | 有（延迟批次 + 确认页） |
| 处理云端已删 | 能 | 能 |
| 处理云端改名/移动后的旧 STRM | 能 | **不能**（pickcode 仍在云端集合，旧文件被保留） |

### 12.3 推荐：反转默认 + 补一个判定，同一版本完成

1. **默认值**：任务级 path-level 默认 `false`；全局 pickcode 级默认 `"auto_clean"`，并在设置页补一个下拉（关闭 / 仅标记 / 自动清理）——现在它是隐藏配置，用户根本调不到。
2. **给 pickcode 清理补"路径不符"判定**：把 `map[pickcode]expectedRelPath` 传进 `cleanupOrphanStrms`，本地 STRM 的 pickcode 在集合里、但相对路径与期望不一致 → 判为改名/移动残留，走同一套阈值。约 30 行，补上 12.2 里唯一的短板。
3. **迁移**：`FullSyncCleanupOrphans == "" && RemoveExtraFiles == true` → 视为"从未配置过 pickcode 清理"，一次性切换到新默认，写日志 + 更新日志说明。
4. **下一版本**再删路径级实现（-104 行）。

### 12.4 为什么不这一版就删路径级

- 迁移后需要观察一个版本的真实反馈；
- 先让 pickcode 级具备"改名去重"能力，功能对齐后再删，避免能力真空。

---

## 13. 待确认的执行顺序

| 批次 | 内容 | 依赖 | 风险 |
|---|---|---|---|
| P2a | 下载幂等（存在且大小一致跳过） | 无 | 零 |
| P2b | 删除增量（-130 行 + 前端开关 + 设置迁移） | P2a | 中 |
| P3a | 清理默认反转 + 设置页补入口 + 配置迁移 | 无 | 中（行为变化） |
| P3b | pickcode 清理补"路径不符"判定（+30 行） | P3a | 低 |
| P3c | 生活事件 3 个细节 | 无 | 低 |
| P4 | 删路径级实现（-104 行） | P3a/P3b 观察一版 | 中 |

以上均为方案，等你确认后再动代码。

### P2 + P3（已完成，2026-10-18）

| 批次 | 内容 | 结果 |
|---|---|---|
| P2a | 下载幂等：本地存在且大小一致 → 跳过（连取直链请求也省掉） | `runDownloads` +18 行 |
| P2b | 删除增量：分支、增量清理、`localArtifactExists`、2 个设置项、前端开关与文案、阶段徽章 | 净减 ~130 行 |
| P3a | 清理默认反转：任务级路径清理默认 false（存量任务一次性迁移，标记文件 `.cleanup-path-disabled`）；pickcode 级默认 `auto_clean` 并在设置页暴露下拉；删除死配置 `settings.removeExtraFiles` | +迁移 2 处 |
| P3b | pickcode 清理改为 `pickcode → 期望本地路径`：路径不一致的旧 STRM 判残留（补上改名/移动去重）；仅快照知道的 pickcode 一律保留 | +40 行 |
| P3c | 单文件 create/move 事件 pickcode 解析失败时重试 2 次（2s/5s），覆盖"整理刚完成 pickcode 还没生成" | +25 行 |

P3c 的另外两项经复核**不需要改**：
- 405 降级：`PullEvents` 已按页交替 proapi/ios ↔ webapi，单页失败还会 `switchApiHost` 重试一次，覆盖度已够。
- 关联资产迁移：同目录 rename 由 `moveOrRenameRelatedAssets` 处理，跨目录 move 由 `event_move.go:628-650` 的同名前缀搬运处理。

测试：`TestCleanupOrphanStrms_StalePathDetected` / `_SnapshotOnlyKept` / `TestReadSettings_MigratesCleanupDefaults` / `TestReadTasks_DisablesPathCleanupOnce`。

### P4（待办）

删除路径级清理实现（`removeExtraFiles`，~104 行）。建议观察一个版本：确认 P3b 的路径判定在真实改名/移动场景下没有误判后再删。
