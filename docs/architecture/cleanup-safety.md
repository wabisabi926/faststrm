# 清理安全边界

清理/删除是破坏性操作，代码遵循以下不变量：

1. **托管归属**：只有 `managed_strms` 中登记、或内容/路径能证明由本工具生成的 STRM 才允许自动删除。
2. **指纹校验**：删除前校验文件大小、mtime、SHA256 与登记一致；用户手改过的文件默认保留。
3. **云端复核**：删除前重新检查云端文件/目录状态；`state=false` 视为失败，不当成空目录。
4. **扫描快照一次性**：`scanId` 绑定一次执行；重复执行返回 `SCAN_TOKEN_USED`，必须重新扫描。
5. **回收站**：删除统一进入 `.faststrm-trash/<session>`，支持恢复；过期清理按映射根限频。
6. **路径安全**：清理、补建、恢复、清空目录拒绝符号链接父目录；拒绝真实 `..` 路径段，允许结尾分隔符。
7. **失败保护**：全量清单缺 count/分页不足时回退逐目录；目录列失败时该子树不参与清理对账。

相关代码：

- `internal/service/strm/managed.go`
- `internal/handler/strm_cleanup.go`
- `internal/handler/strm_cleanup_trash.go`
- `pkg/strmutil/safedelete.go`
- `pkg/strmutil/trash.go`

修改清理逻辑时，必须同时补充：

- 误删保护单测（未托管、手改、云端异常、清单不完整）
- 回收站恢复单测
- 真实库扫描验证；**不要**在真实库上执行破坏性删除测试。
