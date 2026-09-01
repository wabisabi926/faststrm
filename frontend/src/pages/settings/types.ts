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
};

export type Settings = {
  "user-agent"?: string;
  strmExtensions?: string[];
  downloadExtensions?: string[];
  mediaMountPath?: string[];
  // 全局 STRM 生成设置
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  removeExtraFiles?: boolean;
  // STRM 智能路由策略配置（v1.2.3+ 始终生效，后端自动决策）
  strm?: {
    forceProxyUaTokens?: string[];
    accountProxyConcurrencyLimit?: number;
    redirectCheckTimeoutMs?: number;
    // T9: STRM URL HMAC-SHA256 签名
    enableTokenSigning?: boolean;
    tokenSecret?: string;
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
    linkMaxPerSecond?: number;
    linkMaxConcurrent?: number;
    downloadMaxConcurrent?: number;
    autoDownloadMetadata?: boolean;
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
};

// 媒体挂载路径相关类型（在主组件和 BasicSettingsTab 间共享）
export type MountSourceTag = "global_302" | "task" | "life_monitor";
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
  nginx: { attempted: boolean; available: boolean; ok: boolean; message: string };
  error?: string;
} | null;

// 监控验证结果类型（在主组件和 MonitorSettingsTab 间共享）
export type VerifyResult = {
  success: boolean;
  message: string;
  perAccount: { account: string; success: boolean; message: string; details?: Record<string, unknown> }[];
} | null;

export type DisplayMonitorState = (MonitorState & { pending?: boolean })[];
