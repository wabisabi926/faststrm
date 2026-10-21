// 清理模块的默认值与标签：唯一来源。
// 此前这些默认值在 CleanupPolicyCard / cleanup.tsx 里各硬编码一份，
// 改后端默认值时容易漏改前端（P2-5）。
// 数值必须与 internal/model/settings.go 的 DefaultSettings().Cleanup 保持一致。

export const CLEANUP_DEFAULTS = {
  /** 单次删除绝对上限（个） */
  maxThreshold: 1000,
  /** 单次删除占本地 STRM 总数的比例上限 */
  maxRatio: 0.1,
  /** 单次删除涉及的目录数上限 */
  maxDirs: 20,
  /** 超过该数量转入待确认批次（后端 DefaultSettings 为 5） */
  stableThreshold: 5,
  /** 二次确认模式：none 立即删除 / plugin_ui Web UI 待确认 / telegram 通知按钮确认 */
  confirmMode: "none",
} as const;

export const AUTO_CLEANUP_LABELS: Record<string, string> = {
  off: "关闭",
  mark_only: "仅标记",
  auto_clean: "自动清理",
};

export const AUTO_MODE_HINTS: Record<string, string> = {
  off: "不检查、不处理。",
  mark_only: "只写日志，不删除文件。",
  auto_clean: "按下方阈值清理；命中保护阈值时停止或转入待确认队列。",
};

export const CLEANUP_CONFIRM_LABELS: Record<string, string> = {
  none: "关闭",
  plugin_ui: "Web UI",
  telegram: "Telegram",
};
