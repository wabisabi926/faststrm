# STRM 清理模块梳理与优化方案

> 调研日期：2026-10-21　基线：`26efadf`
> 范围：前端 `pages/cleanup.tsx` + `pages/strm-cleanup/`（1530 行）、后端 `handler/strm_cleanup.go`（2008 行）+ `task/executor_utils.go` 的清理逻辑
> 状态：**待确认，未改代码**
> 进度复核（2026-10-21，`e237033`）：**9 个批次均未开工**，P0-1/P0-2/P0-3 与 P1-1~P1-5 已逐条核对仍存在。
> 详见 `执行历史与运行日志梳理方案.md` 第一节的复核表与优先级建议。

---

## 零、总体判断

清理模块功能是完整的（扫描 → 执行 / 待确认批次 / 回收站恢复 / 云端复核 / 托管校验），但存在 **3 个正确性问题**（其中 1 个会让配了文件名模板的用户清理功能完全失效），以及一批重复实现。

按优先级分四类：

| 级别 | 数量 | 性质 |
|---|---|---|
| P0 | 3 | 正确性 / 功能失效，建议立即修 |
| P1 | 5 | 功能缺陷，影响使用 |
| P2 | 8 | 精简 / 合并 / 体验 |
| P3 | 3 | 可增加 |

---

## 一、P0 正确性问题

### P0-1　对账键不认识 `StrmFilenameTemplate`（最严重）

**证据**：
- `handler/strm_cleanup.go` 全文搜索 `StrmFilenameTemplate` / `resolveStrmRelPath`：**零命中**
- 对账键固定走 `cloudStrmRelKey(cloudRel)` → `cleanupStrmFileName` → `{stem}.strm`（`:1188-1214`）
- 生成侧 `resolveStrmRelPath(template, account, f)`（`executor_utils.go:110`）：配了模板时用 `[{account}] {stem}.strm` 之类

**后果**：
配置了「STRM 文件名模板」的用户，本地文件名形如 `[小号] 沙丘.strm`，而清理侧认为应该叫 `沙丘.strm` →
**所有本地 STRM 都被判为 `not_in_cloud`（stale）**。默认比例保护（10%）会拦下来进入保护态，但清理功能对这类用户**完全失效**（每次扫描都是全库 stale），且一旦用户调高阈值就会真删。

**修法**：
把 `resolveStrmRelPath` 拆成「纯路径版本」`ResolveStrmRelPathByTemplate(template, account, cloudRel)`，清理侧 `cloudStrmRelKey` 改为接收 settings 后调用它。生成侧与清理侧从此共用同一份实现（当前是两套各写一遍）。

---

### P0-2　回收站恢复后文件永久不可再清理

**证据**：
- `registerRestoredManagedSTRM`（`strm_cleanup_trash.go:241-260`）调用 `ManagedStrm.Record` 时**没有传 `CloudPath`**
- `ManagedInput` 有 `CloudPath` 字段（`strm/managed.go:24`）
- `UpsertManagedStrm` 的冲突分支：`cloud_path=excluded.cloud_path`（`db/managed_strms.go:77`）→ 把已有 cloud_path 覆盖成空
- 后续删除前复核：`cloudPath == ""` → `SkippedCloudUnverified`（`strm_cleanup.go:1542`、`cloudcheck.go:71`）

**后果**：
用户把某个 STRM 从回收站恢复后，该文件在托管表里的云端路径被清空 → 以后每次清理都因"缺少云端路径"跳过它。**这个文件永远不会被自动清理**，且扫描结果里会显示"跳过 N 个"。

**修法**（二选一，建议都做）：
1. `registerRestoredManagedSTRM` 反推 CloudPath（从 STRM 内容 + 本地路径还原云端相对路径）
2. `UpsertManagedStrm` 对空值改用 `COALESCE(NULLIF(excluded.cloud_path,''), managed_strms.cloud_path)`，避免任何调用方漏传时清空已有数据（防御性）

---

### P0-3　扫描与补生成不过滤媒体规则

**证据**：
- 扫描时云端条目只跳过 `IsRelatedAssetPath`（图片/字幕/nfo 等），见 `strm_cleanup.go:713-720`
- **没有**套用：`StrmExtensions`（视频扩展名）、`StrmGenerateBlacklist`（黑名单）、`minFileSize`（最小体积）、`IsBdmvStreamPath`（原盘目录）
- 补生成 `buildMissingStrmContent`（`:1780-1820`）直接用 `MissingStrm` 数据 BuildContent，同样无过滤

**后果**：
- 云端任何非关联文件（`.txt` / `.zip` / `.rar` / `.torrent`）都会被列为「缺失 STRM」
- 用户点「补生成」→ 给这些文件生成 STRM → Emby 扫描时产生一堆无法识别的条目
- trailer / sample（黑名单）、BDMV/STREAM 下的 m2ts（原盘碎片）同样会被补建

**修法**：
扫描阶段复用生成侧的 `shouldGenerateStrm(name, size, minFileSize, blacklist)` + 扩展名集合 + `model.IsBdmvStreamPath`，把不合格的条目直接排除在 `MissingStrms` 之外。

---

## 二、P1 功能缺陷

| # | 问题 | 证据 | 影响 |
|---|---|---|---|
| P1-1 | **`RemoveStrm=false` 时前端假成功** | 后端只在 `RemoveStrm` 为真时 `DeletedCount++`（`strm_cleanup.go:1593`）；前端无条件 `toast.success` 并从列表移除选中项（`useStrmCleanup.ts:242-267`） | 用户关掉「删除 STRM 本身」后，点删除显示"已删除 0 个"的成功提示、条目从 UI 消失，刷新后又回来 |
| P1-2 | **待确认批次执行不校验过期** | `HandleStrmCleanupPendingExecutePOST`（`:1975`）无 BatchMaxAge 校验；只有 TG 回调校验（`cleanup_interaction.go:338`） | Web UI 里可以执行几天前的批次，与"24h 过期"文案不符 |
| P1-3 | **`MarkUsed` 早于阈值判定** | 扫描票在 `:405` 就被标记使用，而阈值判定/入队在其后 | 触发保护态或入队等待确认时，用户的扫描票已作废 → 只能重新扫描才能再操作 |
| P1-4 | **删除逻辑 4 份重复** | `deleteSelectedStale`（`:1564`）、`deleteAllStaleFromScan`（`:1627`）、`executePendingBatch`（`:1858`）、`cleanupOrphanStrms`（`executor_utils.go:926-1010`），每份都重复「VerifyFile → 云端复核 → MoveToTrash → MarkDeleted → 关联文件 → 空目录」约 40 行 | 修一个 bug 要改 4 处；P0-2 这类问题正是分散实现导致的 |
| P1-5 | **Scan 与 Preview 两份读文件实现** | `readStrmHead`（`:976`）与 Preview 内联读（`:1059-1079`） | 只抽公共函数 `readStrmBytes(path, maxBytes)` 消重复；**接口不合并**（Scan 批量读 512B、Preview 单文件读全文，职责不同） |

---

## 三、P2 精简与合并

### 前端（收益最大）

| # | 问题 | 位置 | 建议 |
|---|---|---|---|
| P2-1 | 标题重复三层 | `cleanup.tsx:73-76`、`:98-104`、`StrmCleanupCard.tsx:75-81` | 删中间那层 section 标题 |
| P2-2 | `policySummary` 与策略卡重复，且取**未保存**的 `data` | `cleanup.tsx:63-67` | 删除（未保存就宣称"当前策略"是误导） |
| P2-3 | 三个刷新按钮语义不清 | 页头 `cleanup.tsx:78`（只刷 settings）、`PendingBatchesSection.tsx:115`、`TrashSection.tsx:76` | 页头刷新改为刷新整页（settings + pending + trash + 最近扫描结果） |
| P2-4 | 5 个删除确认弹窗重复 | `CleanupToolbar.tsx:67/104/142`、`StaleStrmDialog.tsx:150`、`PendingBatchesSection.tsx:177`、`MissingStrmDialog.tsx:107` | 抽一个 `ConfirmDeleteDialog`，统一「移入 .faststrm-trash」文案 |
| P2-5 | 默认值硬编码 3 份、标签映射 2 套 | `CleanupPolicyCard.tsx:109/124/138/151`、`cleanup.tsx:65`、`cleanup.tsx:10-20` vs `CleanupPolicyCard.tsx:19-23` | 抽 `cleanup-constants.ts`（默认值 + 标签），与后端默认值对齐 |
| P2-6 | 批量操作后全量重扫 + 本地双写 | `useStrmCleanup.ts:197-216`（refreshAfterBulk）与 `:242-267`（本地改 scanResult） | 只保留一条：优先用响应里的 `refreshedStats` 本地更新，避免多一次全量扫描 |
| P2-7 | 回收站只能恢复，无删除/清空 | `TrashSection.tsx` | 补「清空回收站」（后端已有 PurgeTrashTree，只缺入口） |
| P2-8 | 待确认批次执行后不刷新统计与回收站 | `PendingBatchesSection.tsx:85-99` | 执行后回调父级刷新 |

### 后端

| # | 问题 | 位置 | 建议 |
|---|---|---|---|
| P2-9 | `ExecuteRequest.ScanSummary` 是死负载 | 前端仍上传全量 mappings（`useStrmCleanup.ts:372`），后端 `:392` 立即被快照覆盖 | 删字段与上传 |
| P2-10 | `ConfirmMode` 的 `plugin_ui` 等价于"非 none" | 只有 `telegram` 有独立分支 | 枚举收敛为 `none / telegram` |
| P2-11 | `RemoveStrm` 在 TG 链路硬编码 true | `adapters.go:608` | 改为读配置 |

---

## 四、P3 可增加

1. **扫描结果按原因分组**：现在 stale 只有 `not_in_cloud` 与账号不匹配两类混在一起，建议分开显示（"云端已删" / "账号已不存在"），用户能判断该不该删。
2. **回收站清空入口**（与 P2-7 同）。
3. **清理影响预估**：执行前显示"将删除 X 个 STRM、Y 个关联文件、Z 个空目录"，比现在的二次确认更直观。

---

## 五、明确不做

| 不做 | 理由 |
|---|---|
| 恢复「按路径清理」 | v1.4.3 已下线（`b1b0c72`），pickcode 对账更准确 |
| 重写扫描为并发 | 清理是低频操作，收益低于风险 |
| 把 4 份删除逻辑彻底重构成一个通用管线 | 建议先做（P1-4）中「抽公共函数」的轻量版，不做跨包大重构 |
| 合并 Scan / Preview 两个接口 | 职责不同（批量读 512B vs 单文件读全文），合并只会给 Scan 加分支；只抽公共读函数 |
| `relatedFileCandidates` 的前缀匹配改动 | 已验证有 `rest 必须以 . 开头` 的保护，"Movie 2.nfo" 不会被误伤，无需改 |

---

## 六、执行计划

### 6.1 决策记录（2026-10-21 确认）

| # | 待确认项 | 决策 | 理由 |
|---|---|---|---|
| 1 | P0-1 修法 | **做**：`cloudStrmRelKey` 增加 `template` / `account` 参数，内部调用从生成侧抽出的纯路径函数；生成侧改调同一函数 | 实际只有 2 个调用点（`strm_cleanup.go:718`、`:747`），且都在 mapping 循环内 `m.Account` 直接可用，成本 40 min；必须补单测锁定"清理侧与生成侧逐字节一致" |
| 2 | P1-4 力度 | **只合并 handler 内 3 处**，task 包不动 | 三处高度同构；`cleanupOrphanStrms` 带 SSE/阈值/入队/重试，强行统一会变成参数爆炸的函数，比重复更糟。等出现第二个调用方再抽 |
| 3 | P1-5 Scan/Preview | **不合并接口**，只抽公共读函数 `readStrmBytes(path, maxBytes)` | Scan 是批量读 512B、Preview 是单文件读全文（`StaleStrmDialog` 的「查看完整」按钮），职责不同；合并只会给 Scan 加分支无收益。抽函数消重复即可，10 min |
| 4 | P2 前端精简 | **分两批**：先做低风险三项，验证后再做重构三项 | 第二批要动 5 个弹窗状态机与 `scanResult` 本地更新路径，最易回归；先拿第一批收益 |

### 6.2 执行批次

| 批次 | 内容 | 预估 | 提交 |
|---|---|---|---|
| 1 | P0-1 对账键套模板（抽纯路径函数 + 单测） | 40 min | `fix(cleanup)` |
| 2 | P0-2 恢复补 CloudPath + upsert 空值防御（COALESCE） | 30 min | `fix(cleanup)` |
| 3 | P0-3 扫描/补生成套媒体规则（扩展名/黑名单/minFileSize/BDMV） | 40 min | `fix(cleanup)` |
| 4 | P1-1 假成功修复 + P1-2 批次过期校验 + P1-3 MarkUsed 时机 | 55 min | `fix(cleanup)` |
| 5 | P1-4 抽 `deleteOneStaleStrm`（handler 3 处合并）+ P1-5 抽 `readStrmBytes` | 100 min | `refactor(cleanup)` |
| 6 | P2 第一批：删重复标题、删 `policySummary`、统一刷新语义 | 30 min | `refactor(web)` |
| 7 | P2 第二批：抽 `ConfirmDialog`、抽常量、消除批量后双写 | 90 min | `refactor(web)` |
| 8 | P2-7/P2-8 回收站清空 + 待确认执行后刷新 | 40 min | `feat(web)` |
| 9 | P3 扫描结果按原因分组 | 40 min | `feat(web)` |

**总计约 7.5 小时**，建议 4 个提交：批次 1-3（P0）、批次 4-5（P1）、批次 6-7（P2 前端）、批次 8-9（P3）。

### 6.3 每批的验收标准

| 批次 | 验收 |
|---|---|
| 1 | 新增单测：配置 `strmFilenameTemplate` 时，`cloudStrmRelKey` 结果 == 生成侧 `resolveStrmRelPath` 结果；老配置（无模板）行为不变 |
| 2 | 单测：恢复登记后 `managed_strms.cloud_path` 非空；`UpsertManagedStrm` 传空 cloud_path 时不清空旧值 |
| 3 | 单测：`.txt` / `trailer.mkv` / BDMV 下 m2ts 不出现在 `MissingStrms` |
| 4 | 手动：`removeStrm=false` 时前端提示"未删除 STRM（仅清理关联文件）"且不假成功；过期批次执行返回 400 |
| 5 | `go test ./...` 全通过；handler 内删除逻辑只剩一处实现 |
| 6-7 | `npx tsc -b` + `npm run build` 通过；页面无重复标题，刷新按钮行为一致 |
| 8-9 | 回收站可清空；待确认执行后统计自动更新 |


---

## 七、实施记录（2026-10-21，批次 1-4 已完成）

| 批次 | 结果 |
|---|---|
| P0-1 对账键套模板 | 新增 `model.RenderStrmRelPath(template, account, cloudRel)` 作为**唯一实现**：生成侧 `task.resolveStrmRelPath` 与 `replaceRelPathExtToStrm` 改为委托，清理侧 `cloudStrmRelKey(cloudRel, template, account)` 在 template 非空时走同一函数。扫描时通过 `strmScanRules.filenameTemplate` 传入，account 取 `m.Account`。测试 `TestCloudStrmRelKey_WithFilenameTemplate` 锁定「清理侧 == 生成侧」且 `[小号] 沙丘.iso.strm` 双扩展名正确。 |
| P0-2 恢复补 CloudPath | `UpsertManagedStrm` 的冲突分支改为 `cloud_path=COALESCE(NULLIF(excluded.cloud_path,''), managed_strms.cloud_path)`：任何调用方漏传都不会清空已有云端路径。回收站恢复（`registerRestoredManagedSTRM` 不传 CloudPath）因此自动保留旧值，墓碑记录本就不删行。测试 `TestUpsertManagedStrm_KeepsCloudPathWhenOmitted`。 |
| P0-3 扫描/补生成套媒体规则 | 新增 `handler/strm_gen_filter.go`：`strmGenFilter` 复刻生成侧规则（BDMV/STREAM → 扩展名列表 → 黑名单/最小体积），扫描的「缺失 STRM」方向与 `regenerateMissingStrms` 都套用。**只过滤「缺失」方向**，`cloudStrmSet` 不变——避免把已有 STRM 变成残留。新增 `MappingScanResult.SkippedMissing` 统计被跳过的条目数。测试 `TestStrmGenFilter_*`（11 个路径用例 + 最小体积 + 自定义扩展名 + nil settings）。 |
| P1-1 假成功 | `useStrmCleanup.handleDeleteResult`：`deletedCount === 0` 时不再弹成功、不再从列表移除条目，改为提示「未删除任何 STRM（设置里「删除 STRM 本身」已关闭）」，有连带删除时提示「已清理 N 个关联文件；STRM 本身未删除」。`types.ts` 补 `removedRelatedFiles`。 |
| P1-2 批次过期校验 | `HandleStrmCleanupPendingExecutePOST` 增加 `BatchMaxAge` 校验（与 TG 回调一致），过期返回 410 + 明确文案。 |
| P1-3 MarkUsed 时机 | 新增 `cleanupScanStore.ReleaseUsed`：执行结果为 `Pending` 或被阈值拦截时归还扫描票，用户不必重新扫描即可调整后重试。 |
| 附带修正 | `buildStrmForMissing` 不再用 `stem + .strm` 二次推导文件名——`RelPath` 已是按模板算好的 .strm 路径，二次加工会破坏模板结果（`[小号] 沙丘.strm`）。 |

**未做（按第六节建议顺序）**：P1-4 删除逻辑合并、P1-5 抽 `readStrmBytes`、P2 前端精简、P3 原因分组、P2-7 回收站清空。
