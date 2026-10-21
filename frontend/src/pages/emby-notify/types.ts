// Emby 通知设置类型定义。
// 从 emby-notify.tsx 抽出，便于子模块共享。
// 详见 v1.1.1 改进任务清单 T4。

export interface EmbySettings {
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
  /** 删除本地软链本身（不跟随目标） */
  syncDeleteDeleteSymlink?: boolean;
  /** 按标题模糊匹配清理同名多版本 */
  syncDeleteRemoveVersions?: boolean;
  /** 同步删除 115 网盘源文件（敏感） */
  // 媒体库刷库配置
  refreshOnCreate?: boolean;  // 创建 STRM 后是否刷库
  refreshOnDelete?: boolean;  // 删除 STRM 后是否刷库
  debounceSeconds?: number;   // 刷库防抖秒数
  // Emby 反向代理（PlaybackInfo 拦截，强制 STRM DirectPlay）
  proxyPort?: number;  // 反代监听端口，0 或空 = 不启用
  // 外部播放器按钮（PotPlayer / VLC / Infuse / MPV），默认关
  externalPlayerEnabled?: boolean;
}

export interface TestResult {
  success: boolean;
  message: string;
}

export interface PathMapping {
  embyPath: string;
  cloudPath: string;
  account?: string;
}

export const DEFAULT_NOTIFY_SETTINGS: EmbySettings = {
  notifyMediaAdded: true,
  notifyMediaRemoved: true,
  notifyPlayback: true,
  playbackShowProgress: true,
  syncDeleteEnabled: false,
  syncDeleteDryRun: true,
  syncDeleteNotify: true,
  syncDeleteDeleteSymlink: false,
  syncDeleteRemoveVersions: false,
  refreshOnCreate: true,
  refreshOnDelete: true,
  debounceSeconds: 10,
};
