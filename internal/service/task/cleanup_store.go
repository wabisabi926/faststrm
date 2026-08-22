package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// DeferredCleanupBatch 一次延迟清理批次（task 侧的轻量数据结构）
// 对齐 MoviePilot full_sync_cleanup_confirm_mode：
//   - ConfirmMode != "none" 时，removeExtraFiles 不直接删除，
//     而是将待删路径通过 CleanupBatchSubmitter 入队到持久化队列，
//     等待 UI/Telegram 二次确认（由 handler.StrmCleanupInteraction 实现）。
type DeferredCleanupBatch struct {
	RequestID       string    // 唯一批次 ID（若空则由 SubmitDeferredBatch 生成）
	TaskID          string    // 关联任务 ID
	TargetPath      string    // 目标目录
	Paths           []string  // 待删除文件路径列表
	RemoveStrm      bool      // 是否删除 STRM 文件本身
	RemoveRelated   bool      // 是否删除关联文件（.nfo/.jpg/.srt）
	RemoveEmptyDirs bool      // 是否清理空目录
	ConfirmMode     string    // 二次确认模式：none/plugin_ui/telegram
	CreatedAt       time.Time // 创建时间
}

// CleanupBatchSubmitter STRM 清理延迟批次提交器接口
// 由 handler 包通过 adapter 实现，避免 task → handler 循环依赖。
// 返回 (实际分配的 requestID, error)；若 batch.RequestID 已设置则直接复用。
type CleanupBatchSubmitter interface {
	SubmitDeferredBatch(ctx context.Context, batch DeferredCleanupBatch) (string, error)
}

// GenerateCleanupRequestID 生成 16 字符 hex ID（task 包内通用工具）
// 用于 SubmitDeferredBatch 之前预留 requestID（适配器实现可覆盖）
func GenerateCleanupRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
