// Emby API 类型定义（参考 qmediasync 实现）

export interface EmbyItemDetail {
  Id?: string;
  Name?: string;
  Type?: string;
  SeriesId?: string;
  SeriesName?: string;
  SeasonId?: string;
  SeasonName?: string;
  ParentIndexNumber?: number;
  IndexNumber?: number;
  CommunityRating?: number;
  Overview?: string;
  Genres?: string[];
  People?: Array<{ Name: string; Type: string }>;
  ImageTags?: Record<string, string>;
  ProductionYear?: number;
  DateCreated?: string;
}

export interface EmbyImageTag {
  Primary?: string;
  Backdrop?: string;
  Banner?: string;
  Thumb?: string;
  Logo?: string;
}

// Emby Webhook 事件类型
export interface EmbyWebhookEvent {
  Event: string;
  Item: {
    Id: string;
    Name: string;
    Type: string;
    Path?: string;  // Emby 媒体文件路径（删除同步用）
    ImageTags?: EmbyImageTag;
    SeriesName?: string;
    SeriesId?: string;
    SeasonId?: string;
    ParentIndexNumber?: number;
    IndexNumber?: number;
  };
  User?: {
    Name: string;
    Id: string;
  };
  Session?: {
    DeviceName: string;
    Client?: string;
  };
  PlaybackInfo?: {
    PositionTicks: number;
    PlaySessionId?: string;
    MediaSource?: {
      RunTimeTicks?: number;
    };
  };
}

// 通知相关类型
export type EmbyEventType =
  | 'library.new'
  | 'library.deleted'
  | 'playback.start'
  | 'playback.pause'
  | 'playback.stop';

export type EmbyNotificationType =
  | 'media_added'
  | 'media_removed'
  | 'playback_start'
  | 'playback_pause'
  | 'playback_stop';

// 剧集分组缓冲
export interface EpisodeBuffer {
  seriesId: string;
  seriesName: string;
  seasons: Map<number, number[]>;
  lastUpdated: number;
}

// 播放事件去重
export interface PlaybackCacheEntry {
  timestamp: number;
}
