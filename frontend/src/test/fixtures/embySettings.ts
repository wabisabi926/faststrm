// Emby 通知设置 fixture：与 emby-notify.tsx 的 EmbySettings 接口结构对齐。
// T4 拆分后改为从 @/pages/emby-notify/types 导入类型。
// 详见 v1.1.1 改进任务清单 T2。

export interface EmbySettingsFixture {
  url?: string;
  apiKey?: string;
  notifyMediaAdded?: boolean;
  notifyMediaRemoved?: boolean;
  notifyPlayback?: boolean;
  playbackShowProgress?: boolean;
  playbackShowOverview?: boolean;
  webhookAuth?: string;
  libraryId?: string;
  syncDeleteEnabled?: boolean;
  syncDeletePathMappings?: Array<{ embyPath: string; cloudPath: string; account?: string }>;
  syncDeleteNotify?: boolean;
  syncDeleteDryRun?: boolean;
  refreshOnCreate?: boolean;
  refreshOnDelete?: boolean;
  debounceSeconds?: number;
}

// 默认设置快照：对齐 emby-notify.tsx 的 DEFAULT_NOTIFY_SETTINGS
export const defaultEmbySettings: EmbySettingsFixture = {
  notifyMediaAdded: true,
  notifyMediaRemoved: true,
  notifyPlayback: true,
  playbackShowProgress: true,
  syncDeleteEnabled: false,
  syncDeleteDryRun: true,
  syncDeleteNotify: true,
  refreshOnCreate: false,
  refreshOnDelete: false,
  debounceSeconds: 10,
};

// 完整配置示例：所有字段填充真实样例
export const fullEmbySettings: EmbySettingsFixture = {
  url: "http://emby.local:8096",
  apiKey: "abc123def456",
  notifyMediaAdded: true,
  notifyMediaRemoved: true,
  notifyPlayback: true,
  playbackShowProgress: true,
  playbackShowOverview: false,
  webhookAuth: "wh-secret-token",
  libraryId: "lib-001",
  syncDeleteEnabled: true,
  syncDeletePathMappings: [
    { embyPath: "/mnt/movies", cloudPath: "/movies", account: "main-115" },
    { embyPath: "/mnt/tv", cloudPath: "/tv-shows" },
  ],
  syncDeleteNotify: true,
  syncDeleteDryRun: false,
  refreshOnCreate: true,
  refreshOnDelete: true,
  debounceSeconds: 15,
};

// 测试连接成功响应
export const embyTestSuccess = {
  success: true,
  message: "连接成功",
};

// 测试连接失败响应
export const embyTestFailure = {
  success: false,
  message: "API Key 无效",
};
