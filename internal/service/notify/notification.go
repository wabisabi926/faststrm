package notify

import (
	"context"
	"fmt"
	"time"
)

// ==================== Notification 类型枚举 ====================

// NotificationType 通知类型
type NotificationType string

const (
	TypeSTRMCreate   NotificationType = "strm_create"
	TypeSTRMDelete   NotificationType = "strm_delete"
	TypeSTRMMove     NotificationType = "strm_move"
	TypeSTRMRename   NotificationType = "strm_rename"
	TypeMediaAdded   NotificationType = "media_added"
	TypeMediaDeleted NotificationType = "media_deleted"
	TypePlayback     NotificationType = "playback"
	TypeTaskComplete NotificationType = "task_complete"
	TypeTaskError    NotificationType = "task_error"
	TypeSystemAlert  NotificationType = "system_alert"
)

// NotificationPriority 通知优先级
type NotificationPriority string

const (
	PriorityHigh   NotificationPriority = "high"
	PriorityNormal NotificationPriority = "normal"
	PriorityLow    NotificationPriority = "low"
)

// ==================== Notification 对象 ====================

// Notification 统一通知对象
type Notification struct {
	Type      NotificationType
	Title     string
	Content   string
	Metadata  map[string]string
	ImageURL  string
	ImageFile string
	Priority  NotificationPriority
	Timestamp time.Time
}

// ==================== STRM 通知构建器 ====================

// StrmNotifyBuilder 构建 STRM 相关通知
type StrmNotifyBuilder struct{}

// NewStrmNotifyBuilder 创建 STRM 通知构建器
func NewStrmNotifyBuilder() *StrmNotifyBuilder {
	return &StrmNotifyBuilder{}
}

// ==================== STRM Create ====================

// STRMCreateInput STRM 创建通知的输入参数
type STRMCreateInput struct {
	Account   string
	Kind      string
	CloudPath string
	LocalPath string
	FileSize  int64
}

// BuildCreateNotification 构建 STRM 创建通知（统一走 FormatMessage 三段式渲染）
func (b *StrmNotifyBuilder) BuildCreateNotification(input STRMCreateInput) *Notification {
	displayMeta := map[string]string{
		"账号":   input.Account,
		"类型":   input.Kind,
		"云端路径": input.CloudPath,
		"本地路径": input.LocalPath,
		"时间":   formatTimestamp(),
	}
	if input.FileSize > 0 {
		displayMeta["大小"] = formatFileSize(input.FileSize)
	}
	content := FormatMessage("📺 STRM 已创建", "", displayMeta)
	return &Notification{
		Type:    TypeSTRMCreate,
		Title:   "📺 STRM 已创建",
		Content: content,
		Metadata: map[string]string{
			"account":    input.Account,
			"kind":       input.Kind,
			"cloud_path": input.CloudPath,
			"local_path": input.LocalPath,
			"file_size":  fmt.Sprintf("%d", input.FileSize),
		},
		Priority:  PriorityNormal,
		Timestamp: time.Now(),
	}
}

// ==================== STRM Delete ====================

// STRMDeleteInput STRM 删除通知的输入参数
type STRMDeleteInput struct {
	Account   string
	Kind      string
	CloudPath string
	LocalPath string
}

// BuildDeleteNotification 构建 STRM 删除通知（统一走 FormatMessage 三段式渲染）
func (b *StrmNotifyBuilder) BuildDeleteNotification(input STRMDeleteInput) *Notification {
	displayMeta := map[string]string{
		"账号":   input.Account,
		"类型":   input.Kind,
		"云端路径": input.CloudPath,
		"本地路径": input.LocalPath,
		"时间":   formatTimestamp(),
	}
	content := FormatMessage("🗑️ STRM 已删除", "", displayMeta)
	return &Notification{
		Type:    TypeSTRMDelete,
		Title:   "🗑️ STRM 已删除",
		Content: content,
		Metadata: map[string]string{
			"account":    input.Account,
			"kind":       input.Kind,
			"cloud_path": input.CloudPath,
			"local_path": input.LocalPath,
		},
		Priority:  PriorityHigh,
		Timestamp: time.Now(),
	}
}

// ==================== STRM Move ====================

// STRMMoveInput STRM 移动通知的输入参数
type STRMMoveInput struct {
	Account   string
	Kind      string
	CloudPath string
	LocalPath string
}

// BuildMoveNotification 构建 STRM 移动通知（统一走 FormatMessage 三段式渲染）
func (b *StrmNotifyBuilder) BuildMoveNotification(input STRMMoveInput) *Notification {
	displayMeta := map[string]string{
		"账号":   input.Account,
		"类型":   input.Kind,
		"云端路径": input.CloudPath,
		"本地路径": input.LocalPath,
		"时间":   formatTimestamp(),
	}
	content := FormatMessage("📦 STRM 已移动", "", displayMeta)
	return &Notification{
		Type:    TypeSTRMMove,
		Title:   "📦 STRM 已移动",
		Content: content,
		Metadata: map[string]string{
			"account":    input.Account,
			"kind":       input.Kind,
			"cloud_path": input.CloudPath,
			"local_path": input.LocalPath,
		},
		Priority:  PriorityNormal,
		Timestamp: time.Now(),
	}
}

// ==================== STRM Rename ====================

// STRMRenameInput STRM 重命名通知的输入参数
type STRMRenameInput struct {
	Account   string
	Kind      string
	CloudPath string
	LocalPath string
}

// BuildRenameNotification 构建 STRM 重命名通知（统一走 FormatMessage 三段式渲染）
func (b *StrmNotifyBuilder) BuildRenameNotification(input STRMRenameInput) *Notification {
	displayMeta := map[string]string{
		"账号":   input.Account,
		"类型":   input.Kind,
		"云端路径": input.CloudPath,
		"本地路径": input.LocalPath,
		"时间":   formatTimestamp(),
	}
	content := FormatMessage("✏️ STRM 已重命名", "", displayMeta)
	return &Notification{
		Type:    TypeSTRMRename,
		Title:   "✏️ STRM 已重命名",
		Content: content,
		Metadata: map[string]string{
			"account":    input.Account,
			"kind":       input.Kind,
			"cloud_path": input.CloudPath,
			"local_path": input.LocalPath,
		},
		Priority:  PriorityNormal,
		Timestamp: time.Now(),
	}
}

// ==================== Dispatch 接口 ====================

// NotificationDispatcher 支持发送结构化通知的接口
type NotificationDispatcher interface {
	Dispatch(ctx context.Context, n *Notification) error
}

// ==================== 辅助函数 ====================

// formatTimestamp 格式化当前时间戳
func formatTimestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// formatFileSize 格式化文件大小
func formatFileSize(bytes int64) string {
	if bytes <= 0 {
		return ""
	}
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
