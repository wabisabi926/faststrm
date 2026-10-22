# STRM 生命周期

## 生成

| 入口 | 代码 | 说明 |
|---|---|---|
| 全量/增量任务 | `internal/service/task` | 扫描云端目录，按扩展名/黑名单/最小体积过滤 |
| 生活事件 | `internal/service/monitor` | create/move/rename/delete 触发本地 STRM 变更 |
| 清理页补建 | `internal/handler/strm_cleanup` | 对缺失的 STRM 重新生成 |
| 迁移工具 | `internal/service/strmimport` | 从外部 STRM 方言重写为标准格式 |

统一由 `strm.BuildContent` 生成内容、`strmutil.StrmFileName` 决定文件名：

- 普通：`movie.mkv` → `movie.strm`
- ISO：`movie.iso` → `movie.iso.strm`
- 写入使用 `.tmp` + rename；内容未变化时不写盘，避免 Emby 假重扫。

## BDMV

`model.IsBdmvStreamPath` 命中 `BDMV/STREAM` 时整棵子树跳过，原因是一个原盘会被拆成上百个 m2ts，逐文件生成会污染媒体库。ISO 不受影响。

## 生活事件

- **create**：生成 STRM，写入 files 表并登记 managed。
- **move/rename**：
  - 默认 `local_move`：文件重命名/移动 STRM 并同步内容；文件夹只原子移动目录 + 更新 DB 路径前缀，不递归改内容。
  - `recreate`：删除旧 STRM，在新位置重新生成。
- **delete**：只删除已托管且指纹未变的 STRM，走回收站。
- 事件缺少 pickcode 时按 file_id 反查（DB → `files/info`）。

## 清理

- 全量清单异常时 fail-closed，不进入破坏性清理。
- 清理对账基于托管记录、pickcode 和指纹；外部/手写 STRM 默认保留。
- 待确认批次由扫描 token 绑定，只能执行一次。
- 删除前做云端复核，删除进入回收站，可按映射根恢复。
