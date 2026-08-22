// Package handler 的 cleanup_interaction 子模块：
// STRM 清理待删队列与 TG inline 按钮二次确认交互。
//
// 参考 MoviePilot p115strmhelper/helper/strm/full/interaction.py：
//   - 待删批次入队 / 列出 / 取消 / 执行
//   - Telegram inline 按钮 callback_data 路由
//   - 持久化到 SQLite（复用 filePathDb.sqlite，独立表 cleanup_pending_batches）
package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

// CleanupCallbackPrefix TG 按钮 callback_data 前缀
// 格式：cleanup_confirm|{request_id}|{y|n}
const CleanupCallbackPrefix = "cleanup_confirm"

// MaxSamplePaths TG 通知预览的最大路径数
const MaxSamplePaths = 5

// ==================== 数据结构 ====================

// CleanupBatch 待删批次
type CleanupBatch struct {
	RequestID          string   `json:"requestId"`
	CreatedAt          int64    `json:"createdAt"`
	Paths              []string `json:"paths"`
	SamplePaths        []string `json:"samplePaths"`
	PathCount          int      `json:"pathCount"`
	RemoveStrm         bool     `json:"removeStrm"`
	RemoveEmptyDirs    bool     `json:"removeEmptyDirs"`
	RemoveRelatedFiles bool     `json:"removeRelatedFiles"`
}

// ==================== StrmCleanupInteraction ====================

// StrmCleanupInteraction STRM 清理待删队列与 TG 按钮二次确认交互
type StrmCleanupInteraction struct {
	db       *sql.DB
	bot      *notify.TelegramBot
	settings *store.SettingsStore
	mu       sync.Mutex
}

// NewStrmCleanupInteraction 创建交互器（含 lazy migrate）
func NewStrmCleanupInteraction(db *sql.DB, bot *notify.TelegramBot, settings *store.SettingsStore) (*StrmCleanupInteraction, error) {
	if db == nil {
		// db 为空时返回无持久化的实例（仅内存，不推荐生产使用）
		logger.S().Warnf("[CleanupInteraction] SQLite 未注入，待删批次将不持久化")
	}
	i := &StrmCleanupInteraction{db: db, bot: bot, settings: settings}
	if db != nil {
		if err := i.migrate(); err != nil {
			return nil, fmt.Errorf("migrate cleanup_pending_batches: %w", err)
		}
	}
	return i, nil
}

// SetBot 延迟注入 TelegramBot
// 用于 server 启动时 bot 在 cleanupInteraction 之后才创建的场景：
// 先用 nil bot 创建 cleanupInteraction 注入到 execDeps.CleanupSubmitter，
// 等 notifyDeps.TelegramBot 就绪后再调用 SetBot 启用 TG 按钮通知。
func (i *StrmCleanupInteraction) SetBot(bot *notify.TelegramBot) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.bot = bot
}

// migrate 创建 cleanup_pending_batches 表
func (i *StrmCleanupInteraction) migrate() error {
	if i.db == nil {
		return nil
	}
	_, err := i.db.Exec(`CREATE TABLE IF NOT EXISTS cleanup_pending_batches (
		request_id           TEXT    PRIMARY KEY,
		created_at           INTEGER NOT NULL,
		paths_json           TEXT    NOT NULL,
		sample_paths_json    TEXT    NOT NULL DEFAULT '[]',
		path_count           INTEGER NOT NULL DEFAULT 0,
		remove_strm          INTEGER NOT NULL DEFAULT 1,
		remove_empty_dirs    INTEGER NOT NULL DEFAULT 0,
		remove_related_files INTEGER NOT NULL DEFAULT 0
	)`)
	return err
}

// boolToInt 布尔转 SQLite 整型
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// intToBool SQLite 整型转布尔
func intToBool(v int) bool {
	return v != 0
}

// AppendBatch 入队一批待确认删除记录
func (i *StrmCleanupInteraction) AppendBatch(ctx context.Context, batch CleanupBatch) error {
	if i.db == nil {
		return fmt.Errorf("db not initialized")
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	pathsJSON, _ := json.Marshal(batch.Paths)
	sampleJSON, _ := json.Marshal(batch.SamplePaths)
	_, err := i.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO cleanup_pending_batches
		(request_id, created_at, paths_json, sample_paths_json, path_count, remove_strm, remove_empty_dirs, remove_related_files)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		batch.RequestID, batch.CreatedAt, string(pathsJSON), string(sampleJSON),
		batch.PathCount, boolToInt(batch.RemoveStrm), boolToInt(batch.RemoveEmptyDirs), boolToInt(batch.RemoveRelatedFiles),
	)
	return err
}

// ListBatches 列出所有待确认批次（按创建时间倒序）
func (i *StrmCleanupInteraction) ListBatches(ctx context.Context) ([]CleanupBatch, error) {
	if i.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	rows, err := i.db.QueryContext(ctx,
		`SELECT request_id, created_at, paths_json, sample_paths_json, path_count, remove_strm, remove_empty_dirs, remove_related_files
		FROM cleanup_pending_batches ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var batches []CleanupBatch
	for rows.Next() {
		var b CleanupBatch
		var pathsJSON, sampleJSON string
		var rs, red, rrf int
		if err := rows.Scan(&b.RequestID, &b.CreatedAt, &pathsJSON, &sampleJSON, &b.PathCount, &rs, &red, &rrf); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(pathsJSON), &b.Paths)
		_ = json.Unmarshal([]byte(sampleJSON), &b.SamplePaths)
		b.RemoveStrm = intToBool(rs)
		b.RemoveEmptyDirs = intToBool(red)
		b.RemoveRelatedFiles = intToBool(rrf)
		batches = append(batches, b)
	}
	return batches, nil
}

// CancelBatch 取消一批（仅从队列移除，不删文件）
// 返回是否找到并移除
func (i *StrmCleanupInteraction) CancelBatch(ctx context.Context, requestID string) (bool, error) {
	if i.db == nil {
		return false, fmt.Errorf("db not initialized")
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	res, err := i.db.ExecContext(ctx, `DELETE FROM cleanup_pending_batches WHERE request_id = ?`, requestID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// PopBatch 取出并从队列移除一批（用于执行删除前取出参数）
// 未找到返回 (nil, nil)
func (i *StrmCleanupInteraction) PopBatch(ctx context.Context, requestID string) (*CleanupBatch, error) {
	if i.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	i.mu.Lock()
	defer i.mu.Unlock()

	var b CleanupBatch
	var pathsJSON, sampleJSON string
	var rs, red, rrf int
	err := i.db.QueryRowContext(ctx,
		`SELECT request_id, created_at, paths_json, sample_paths_json, path_count, remove_strm, remove_empty_dirs, remove_related_files
		FROM cleanup_pending_batches WHERE request_id = ?`,
		requestID,
	).Scan(&b.RequestID, &b.CreatedAt, &pathsJSON, &sampleJSON, &b.PathCount, &rs, &red, &rrf)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(pathsJSON), &b.Paths)
	_ = json.Unmarshal([]byte(sampleJSON), &b.SamplePaths)
	b.RemoveStrm = intToBool(rs)
	b.RemoveEmptyDirs = intToBool(red)
	b.RemoveRelatedFiles = intToBool(rrf)

	// 从队列移除
	_, _ = i.db.ExecContext(ctx, `DELETE FROM cleanup_pending_batches WHERE request_id = ?`, requestID)
	return &b, nil
}

// ==================== Telegram 通知 ====================

// NotifyTelegramPending 发送 TG 待确认通知（含 inline 按钮）
func (i *StrmCleanupInteraction) NotifyTelegramPending(ctx context.Context, batch CleanupBatch) error {
	if i.bot == nil {
		logger.S().Warnf("[CleanupInteraction] TG bot 未注入，跳过通知 request_id=%s", batch.RequestID)
		return nil
	}
	if i.settings == nil {
		return fmt.Errorf("settings store 未注入")
	}
	s, err := i.settings.ReadSettings()
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	if !s.Telegram.Enabled {
		logger.S().Debugf("[CleanupInteraction] Telegram 未启用，跳过通知 request_id=%s", batch.RequestID)
		return nil
	}
	chatID := s.Telegram.ChatID
	if chatID == "" {
		return fmt.Errorf("telegram chatId 为空")
	}

	var sb strings.Builder
	sb.WriteString("🧹 <b>STRM 清理待确认</b>\n\n")
	sb.WriteString(fmt.Sprintf("<b>待删文件数:</b> %d\n", batch.PathCount))
	if len(batch.SamplePaths) > 0 {
		sb.WriteString("\n<b>预览（前 5 个）:</b>\n")
		for _, p := range batch.SamplePaths {
			sb.WriteString("  • <code>" + p + "</code>\n")
		}
	}
	sb.WriteString(fmt.Sprintf("\n⏰ %s", time.Now().Format("2006-01-02 15:04:05")))

	buttons := [][]notify.InlineKeyboardButton{{
		{Text: "✅ 确认删除", CallbackData: fmt.Sprintf("%s|%s|y", CleanupCallbackPrefix, batch.RequestID)},
		{Text: "❌ 取消", CallbackData: fmt.Sprintf("%s|%s|n", CleanupCallbackPrefix, batch.RequestID)},
	}}
	return i.bot.SendMessageWithButtons(ctx, chatID, sb.String(), buttons)
}

// ==================== Callback 解析 ====================

// ParseCleanupCallback 解析 TG 按钮 callback_data
// 格式：cleanup_confirm|{request_id}|{y|n}
// 返回 (requestID, approve, 是否匹配)
func ParseCleanupCallback(data string) (requestID string, approve bool, ok bool) {
	parts := strings.Split(data, "|")
	if len(parts) != 3 || parts[0] != CleanupCallbackPrefix {
		return "", false, false
	}
	if parts[2] != "y" && parts[2] != "n" {
		return "", false, false
	}
	return parts[1], parts[2] == "y", true
}

// IsCleanupCallback 快速判断 callback_data 是否为清理确认
func IsCleanupCallback(data string) bool {
	return strings.HasPrefix(data, CleanupCallbackPrefix+"|")
}

// ==================== 工具函数 ====================

// BuildSamplePaths 从全量 paths 取前 MaxSamplePaths 个作为预览
func BuildSamplePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	n := len(paths)
	if n > MaxSamplePaths {
		n = MaxSamplePaths
	}
	out := make([]string, n)
	copy(out, paths[:n])
	return out
}

// GenerateRequestID 生成待删批次 ID（时间戳 + 短随机后缀）
func GenerateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// ==================== Telegram 按钮 callback 处理器 ====================

// HandleTelegramCallback 实现 notify.CleanupCallbackHandler 接口
// 处理 TG 按钮 callback：cleanup_confirm|{request_id}|{y|n}
// approve=true：确认删除（PopBatch + executePendingBatch）
// approve=false：取消（CancelBatch，仅从队列移除不删文件）
// 返回处理结果消息（用于回复用户）
func (i *StrmCleanupInteraction) HandleTelegramCallback(ctx context.Context, requestID string, approve bool) (string, error) {
	if approve {
		// 确认删除
		batch, err := i.PopBatch(ctx, requestID)
		if err != nil {
			return "", fmt.Errorf("pop batch: %w", err)
		}
		if batch == nil {
			return "⚠️ 待删批次不存在或已被处理（可能已通过 Web UI 执行/取消）", nil
		}
		resp := executePendingBatch(ctx, *batch)
		logger.S().Infof("[CleanupInteraction] TG callback 确认删除 %s: deleted=%d failed=%d dirs=%d duration=%dms",
			requestID, resp.DeletedCount, resp.FailedCount, len(resp.RemovedEmptyDirs), resp.DurationMs)
		var sb strings.Builder
		sb.WriteString("✅ <b>已执行删除</b>\n\n")
		sb.WriteString(fmt.Sprintf("<b>批次:</b> <code>%s</code>\n", requestID))
		sb.WriteString(fmt.Sprintf("<b>待删:</b> %d\n", batch.PathCount))
		sb.WriteString(fmt.Sprintf("<b>已删:</b> %d\n", resp.DeletedCount))
		if resp.FailedCount > 0 {
			sb.WriteString(fmt.Sprintf("<b>失败:</b> %d\n", resp.FailedCount))
		}
		if len(resp.RemovedEmptyDirs) > 0 {
			sb.WriteString(fmt.Sprintf("<b>清理空目录:</b> %d\n", len(resp.RemovedEmptyDirs)))
		}
		if len(resp.RemovedRelatedFiles) > 0 {
			sb.WriteString(fmt.Sprintf("<b>清理关联文件:</b> %d\n", len(resp.RemovedRelatedFiles)))
		}
		return sb.String(), nil
	}

	// 取消删除
	ok, err := i.CancelBatch(ctx, requestID)
	if err != nil {
		return "", fmt.Errorf("cancel batch: %w", err)
	}
	if !ok {
		return "⚠️ 待删批次不存在（可能已被处理）", nil
	}
	logger.S().Infof("[CleanupInteraction] TG callback 取消删除 %s", requestID)
	return fmt.Sprintf("✅ <b>已取消删除</b>\n\n<b>批次:</b> <code>%s</code>\n文件未被删除，可稍后重新扫描", requestID), nil
}
