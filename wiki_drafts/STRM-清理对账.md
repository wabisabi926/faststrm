# STRM 清理对账

清理对账用于发现本地 STRM 与 115 云端不一致的情况，并在可控、可恢复的前提下处理：

- 本地有 STRM，云端文件已不存在（孤儿）
- 云端有文件，本地缺少 STRM（漏生成）
- 云端文件仍在，但本地路径与云端不一致（改名/移动残留）

> 本页面适用于 v1.5.0 及以上版本。旧版的路径级清理、增量清理、`.deleted.bak` 软删、100 条熔断均已下线。

---

## 一、所有权模型

v1.5.0 起，清理只处理**本工具生成的 STRM**。

- 写入 STRM 时登记到 SQLite `managed_strms`：本地路径、账号、云端路径、pickcode、file_id、SHA256、size、mtime、来源
- 历史文件启动时异步纳管；只纳管满足以下条件的文件：
  - 内容是标准 `/api/strm` 或 `/api/fs/get` URL
  - pickcode 合法（17 位字母数字）
  - 账号存在，且该 pickcode 在该账号的云端索引中
- 手写 STRM、第三方工具生成、无法确认来源的文件保持**未托管**

扫描结果分三类：

| 分类 | 含义 | 默认处理 |
|---|---|---|
| 托管孤儿 | 本地路径已登记，但云端不存在 | 进入删除候选 |
| 未托管 | 云端不存在，但本地未登记 | 只展示数量，不删除 |
| 漏生成 | 云端有文件，本地没有对应 STRM | 进入补生成候选 |

> 升级后第一次打开清理页，如果历史纳管仍在后台执行，未托管数量可能偏高；稍后重新扫描即可。

---

## 二、扫描与 scan token

### 扫描范围

使用「生活事件监控」中已配置的路径映射（账号 + 云端目录 + 本地目录），也允许任务目标目录。扫描会：

1. 递归列出云端目录，按生成侧落盘规则换算本地 STRM 相对路径
2. 遍历本地 `.strm`（跳过 `.faststrm-trash`）
3. 对比托管表与云端集合，得到托管孤儿 / 未托管 / 漏生成

### scanId

扫描成功后返回 `scanId` 与服务端快照（默认 30 分钟有效，最多 8 份，按用户绑定）。

执行清理必须携带 `scanId`：

- `delete` / `delete_and_regenerate`：客户端选中的路径必须存在于快照的托管孤儿集合
- `delete_all` / `regenerate`：完全使用服务端快照，忽略客户端提交的扫描摘要
- 快照不存在/过期 → 返回 `SCAN_TOKEN_INVALID`，需要重新扫描
- 路径不在快照中 → 返回 `SCAN_STALE_MISMATCH`

这样即使前端伪造路径或扫描摘要，也只能操作本次扫描确认过的范围。

---

## 三、删除保护

### 三重保护

全局清理策略由 `download.fullSyncCleanupOrphans` 控制：`off` / `mark_only` / `auto_clean`，默认 `auto_clean`。即使选择 `auto_clean`，也只处理托管 STRM，并在删除前做云端复核。

| 配置 | 默认值 | 触发后 |
|---|---|---|
| `maxRatio` | 0.10（10%） | 进入保护态，拒绝删除 |
| `maxThreshold` | 1000 | 进入保护态，拒绝删除 |
| `maxDirs` | 20 | 进入保护态，拒绝删除 |
| `stableThreshold` | 5 | 超过后进入二次确认/待删队列 |

- 小于等于 `stableThreshold` 的小批量直接执行
- 手动选中删除与 `delete_all` 都走保护判定
- `delete_all` 的比例基数取本地 STRM 总数，避免“一次全删”

### 待确认批次

`confirmMode` 不为 `none` 且超过稳定阈值时，删除会进入 `cleanup_pending_batches`：

- 24 小时内未确认自动作废
- 可在 Web UI 清理页或 Telegram 按钮中确认/取消
- 执行前再次做托管归属、指纹与云端复核

---

## 四、删前复核

每个删除动作在移入回收站前执行：

1. **托管归属**：本地路径存在 active 托管记录
2. **指纹**：size / mtime / SHA256 与登记时一致
3. **云端复核**：按托管记录的 `cloud_path` 列出父目录，确认原文件名与 pickcode 均不在目录中
4. **复核失败处理**：目录查询失败、账号 Cookie 缺失、`cloud_path` 缺失时一律保留，不删除

结果字段：

- `skippedUnmanaged`：未托管/指纹变化
- `skippedCloudRestored`：云端文件已恢复
- `skippedCloudUnverified`：云端路径缺失或复核失败

> 云端复核会缓存 15 秒内的目录列表，只覆盖同一次删除循环；跨请求会重新查询。
> 早期 move/rename 生成的部分托管记录可能没有 `cloud_path`，这类文件会判为“未确认”并保留；重新生成/重扫后可补全。

---

## 五、回收站

删除不再直接物理删除，而是移动到：

```
<映射根>/.faststrm-trash/<时间戳>/<原相对路径>
```

- 保留 7 天，自动清理过期批次
- `.faststrm-trash` 不参与扫描/统计/Emby 媒体库
- 清理页提供回收站列表与一键恢复
- 恢复时若原路径已有文件，返回 409，不会覆盖；API 显式 `overwrite=true` 会先把现有文件移入回收站
- 恢复 `.strm` 后自动重新登记托管

---

## 六、关联文件与空目录

- `removeRelatedFiles`：删除孤儿 STRM 时，连带移动同名 `.nfo/.jpg/.jpeg/.png/.webp/.srt/.ass/.ssa/.sub/.vtt/.sup` 到回收站
  - 只按 `stem` 前缀 + `.` + 白名单扩展名匹配，不碰视频文件
  - `movie.zh.srt` 这类多段后缀会正确匹配
- `removeEmptyDirs`：删除完成后向上回收空目录，直到映射根为止

> v1.5.0 起两个开关**默认关闭**；升级时 `CleanupDefaultsV3` 会把旧版本自动打开过的值关闭一次。需要时请在设置页重新勾选。

---

## 七、接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/strmCleanup/scan` | 扫描并返回 `scanId`、托管孤儿、未托管、漏生成 |
| POST | `/api/strmCleanup/execute` | 执行删除/补生成，必须带 `scanId` |
| POST | `/api/strmCleanup/preview` | 预览本地 STRM 内容 |
| GET | `/api/strmCleanup/pending` | 待确认批次 |
| POST | `/api/strmCleanup/pending/cancel` | 取消批次 |
| POST | `/api/strmCleanup/pending/execute` | 确认执行 |
| GET | `/api/strmCleanup/trash` | 回收站列表 |
| POST | `/api/strmCleanup/trash/restore` | 恢复回收站文件 |

所有接口需要 Web 登录 JWT；清理范围只允许已配置的映射根或任务目标目录。

---

## 八、常见问题

**扫描显示大量“未托管”**
历史纳管可能仍在后台进行，或文件是手写/第三方生成的。未托管不会被自动删除；确认不需要时可手动处理。

**执行提示“扫描结果不存在或已过期”**
扫描快照默认 30 分钟有效。重新扫描后立即执行即可。

**文件没有被删除，提示“保留”**
查看结果里的 `skippedCloudRestored` / `skippedCloudUnverified`：
- 云端文件已恢复：这是预期保护
- 云端复核失败：检查账号 Cookie、网络和 `cloud_path` 是否存在
- 未托管/指纹变化：重新扫描或重新生成后再处理

**回收站文件怎么恢复**
清理页「回收站」→ 找到原路径 → 恢复。也可以手动把 `.faststrm-trash` 中的文件移回原位置，但建议使用 UI/API 以同步托管登记。

**没有启用 SQLite**
自动清理会降级为“仅标记”，不会删除文件。请启用 SQLite 并重新扫描。
