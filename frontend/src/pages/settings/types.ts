export type PathMapping = {
  account?: string;
  cloudPath: string;
  localPath: string;
};

export type LifeMonitorConfig = {
  enabled: boolean;
  accounts: string[];
  pollInterval: number;
  pathMappings: PathMapping[];
  removeEmptyDirs: boolean;
  notifyOnlyOnError?: boolean;
  eventTypes: {
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  };
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  minFileSize?: number;
  firstPullMode?: "latest" | "all" | "last";
  moveMediaMode?: "recreate" | "local_move";
  // 移出映射（移到非媒体目录）时是否删除本地旧 STRM；默认 true
  moveOutRemoveLocalStrm?: boolean;
};

export type Settings = {
  "user-agent"?: string;
  strmExtensions?: string[];
  downloadExtensions?: string[];
  mediaMountPath?: string[];
  // 全局 STRM 生成设置
  strmPrefix?: string;
  // URL 参数编码开关（默认 true）：关闭后 file_name 等参数原样写入 URL
  enablePathEncoding?: boolean;
  // 清理：阈值、确认模式与连带动作
  cleanup?: {
    maxThreshold?: number;
    maxRatio?: number;
    maxDirs?: number;
    stableThreshold?: number;
    confirmMode?: "none" | "plugin_ui" | "telegram";
    removeStrm?: boolean;
    removeRelatedFiles?: boolean;
    removeEmptyDirs?: boolean;
  };
  // STRM 智能路由策略配置（v1.2.3+ 始终生效，后端自动决策）
  strm?: {
    forceProxyUaTokens?: string[];
    accountProxyConcurrencyLimit?: number;
    redirectCheckTimeoutMs?: number;
    // 需要服务端代理流的容器/扩展名
    forceProxyFormats?: string[];
    // 代理流分片并发（大原盘单连接带宽不足时显著改善）
    disableSeekFormatProxy?: boolean;
    disableRangeProxy?: boolean;
    proxyChunkSizeMB?: number;
    proxyConcurrency?: number;
    // STRM URL 模板：由「标准 URL + 附加参数勾选」生成；空 = 走后端硬编码默认
    strmUrlTemplate?: string;
  };
  // 115 API 限流（服务启动时生效，修改后需重启）
  rateLimit?: {
    api115Qpm?: number;
    playbackQpm?: number;
    lifeQpm?: number;
    globalCapQpm?: number;
  };
  emby?: {
    url?: string;
    apiKey?: string;
    notifyMediaAdded?: boolean;
    notifyMediaRemoved?: boolean;
    notifyPlayback?: boolean;
    playbackShowProgress?: boolean;
    playbackShowOverview?: boolean;
    webhookAuth?: string;
    libraryId?: string;
  };
  download?: {
    downloadMaxConcurrent?: number;
    autoDownloadMetadata?: boolean;
    // 全局文件过滤：最小文件大小阈值（字节，0 表示不过滤）
    minFileSize?: number;
    // 全局文件过滤：文件名黑名单关键词（大小写不敏感，如 "*trailer*"）
    strmGenerateBlacklist?: string[];
    // 孤儿 STRM 清理策略："off" / "mark_only" / "auto_clean"（默认）
    fullSyncCleanupOrphans?: string;
    // STRM 覆盖模式："always"(总是覆盖，默认) / "never"(跳过已存在)
  };
  lifeMonitor?: LifeMonitorConfig;
} & Record<string, unknown>;

export type MonitorState = {
  account: string;
  running: boolean;
  status: string;
  eventsProcessed: number;
  lastError?: string;
};

export const DEFAULT_MONITOR_CONFIG: LifeMonitorConfig = {
  enabled: false,
  accounts: [],
  pollInterval: 10,
  pathMappings: [],
  removeEmptyDirs: true,
  notifyOnlyOnError: false, // 默认正常通知
  eventTypes: {
    create: true,
    remove: true,
    rename: true,
    move: true,
  },
  minFileSize: 0,
  firstPullMode: "latest",
  moveMediaMode: "local_move",
  // 移出映射时删除本地旧 STRM（默认开启，符合直觉：移出即清理）
  moveOutRemoveLocalStrm: true,
};

// 媒体挂载路径相关类型（在主组件和 BasicSettingsTab 间共享）
export type MountSourceTag = "global" | "global_302" | "task" | "life_monitor";
export type MountEntryRow = {
  prefix: string;
  source: MountSourceTag;
  sourceLabel: string;
  account?: string;
  taskId?: string;
};
export type MountDryRunData = {
  persisted: string[];
  computed: MountEntryRow[];
  final: string[];
  diff: {
    added: string[];
    removed: string[];
    kept: string[];
    changed: boolean;
  };
} | null;
export type MountSyncApplyData = {
  changed: boolean;
  added: string[];
  removed: string[];
  final: string[];
  nginx: { attempted: boolean; available: boolean; skipped: boolean; ok: boolean; message: string };
  error?: string;
} | null;

// 监控验证结果类型（在主组件和 MonitorSettingsTab 间共享）
export type VerifyResult = {
  success: boolean;
  message: string;
  perAccount: { account: string; success: boolean; message: string; details?: Record<string, unknown> }[];
} | null;

export type DisplayMonitorState = (MonitorState & { pending?: boolean })[];
