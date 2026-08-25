// Emby Webhook 事件分发器 + 通知模板
// 对齐 frontend/src/lib/emby/notifierDispatcher.ts 和 notifierTemplates.ts
// 参考移植：qmediasync (D:\下载\AI\qmediasync) 通知模板的优点
package emby

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/notify"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 常量 ====================

const (
	// EpisodeDebounceWindow 剧集缓冲防抖窗口（10 秒）
	EpisodeDebounceWindow = 10 * time.Second
	// PlaybackDedupWindow 播放事件去重窗口（60 秒）
	PlaybackDedupWindow = 60 * time.Second
	// PlaybackCacheTTL 播放缓存条目 TTL（5 分钟）
	PlaybackCacheTTL = 5 * time.Minute
	// ImageMaxWidth 通知图片默认最大宽度（对齐 qmediasync 竖版海报效果）
	ImageMaxWidth = 720
)

// ==================== Dispatcher 接口 ====================

// NotifierDispatcher 通知发送接口（对齐 TS notifierSender 的 sendEmbyText/sendEmbyWithPoster）
type NotifierDispatcher interface {
	// Notify 发送纯文本通知
	Notify(ctx context.Context, msg string) error
	// NotifyWithPhoto 发送带图片通知（caption 为文本，photoURL 为图片 URL）
	NotifyWithPhoto(ctx context.Context, caption, photoURL string) error
}

// SettingsProvider 返回当前 EmbySettings 的回调
type SettingsProvider func() model.EmbySettings

// ==================== 剧集缓冲 ====================

// episodeBuffer 剧集分组缓冲（入库/删除独立）
type episodeBuffer struct {
	seriesID    string
	seriesName  string
	episodes    []ItemDetail
	lastUpdated time.Time
}

// ==================== Notifier ====================

// Notifier Emby Webhook 事件分发器
type Notifier struct {
	client     *Client
	dispatcher NotifierDispatcher
	settingsFn SettingsProvider

	// 可选：删除同步实例（library.deleted 事件触发）
	syncDelete *SyncDelete

	// 剧集入库缓冲
	addedMu     sync.Mutex
	addedBuffer map[string]*episodeBuffer
	addedTimers map[string]*time.Timer

	// 剧集删除缓冲
	deletedMu     sync.Mutex
	deletedBuffer map[string]*episodeBuffer
	deletedTimers map[string]*time.Timer

	// 播放事件去重
	playbackMu    sync.Mutex
	playbackCache map[string]time.Time
}

// NewNotifier 创建 Notifier
func NewNotifier(client *Client, dispatcher NotifierDispatcher, settingsFn SettingsProvider) *Notifier {
	return &Notifier{
		client:        client,
		dispatcher:    dispatcher,
		settingsFn:    settingsFn,
		addedBuffer:   make(map[string]*episodeBuffer),
		addedTimers:   make(map[string]*time.Timer),
		deletedBuffer: make(map[string]*episodeBuffer),
		deletedTimers: make(map[string]*time.Timer),
		playbackCache: make(map[string]time.Time),
	}
}

// SetSyncDelete 注入 SyncDelete 实例（可选）
func (n *Notifier) SetSyncDelete(sd *SyncDelete) {
	n.syncDelete = sd
}

// ==================== 主事件分发 ====================

// HandleWebhookEvent 主事件分发入口
func (n *Notifier) HandleWebhookEvent(ctx context.Context, event WebhookEvent) error {
	if event.Event == "" {
		return fmt.Errorf("empty event type")
	}
	if event.Item == nil {
		logger.S().Infof("[Emby] 收到事件 %s, 无 Item", event.Event)
		return nil
	}

	logger.S().Infof("[Emby] 收到事件: %s, 项目: %s", event.Event, event.Item.Name)

	// 注意：client 为 nil（Emby 未配置 URL/APIKey）时不全局跳过，
	// 因为播放通知、删除通知有降级逻辑，无需 EmbyClient 也能发送；
	// 入库通知在获取详情失败时也会降级为简版通知。
	// 各 handler 内部已正确处理 client == nil 的情况。

	switch event.Event {
	case "library.new":
		return n.handleMediaAdded(ctx, *event.Item)

	case "library.deleted":
		// 删除同步：删 STRM + 关联文件 + DB 记录（独立于通知逻辑）
		if n.syncDelete != nil && event.Item.Path != "" {
			if err := n.syncDelete.HandleSyncDelete(ctx, *event.Item); err != nil {
				logger.S().Errorf("[Emby] sync delete failed: %v", err)
			}
		}
		// 通知逻辑：若 syncDelete 已开启并启用了通知，则跳过原始删除通知
		settings := n.settingsFn()
		skipNotify := settings.SyncDeleteEnabled && settings.SyncDeleteNotify
		if skipNotify {
			return nil
		}
		return n.handleMediaDeleted(ctx, *event.Item)

	case "playback.start", "playback.pause", "playback.stop":
		return n.handlePlaybackEvent(ctx, event)

	default:
		logger.S().Infof("[Emby] 未处理的事件类型: %s", event.Event)
		return nil
	}
}

// ==================== 入库通知 ====================

// handleMediaAdded 处理入库事件
func (n *Notifier) handleMediaAdded(ctx context.Context, item ItemInfo) error {
	settings := n.settingsFn()
	if !settings.NotifyMediaAdded {
		return nil
	}
	switch item.Type {
	case "Movie":
		return n.handleMovieAdded(ctx, item)
	case "Episode":
		n.handleSeriesEpisodeAdded(ctx, item)
		return nil
	default:
		return nil
	}
}

// handleMovieAdded 处理电影入库（对齐 TS handleMovieAdded）
func (n *Notifier) handleMovieAdded(ctx context.Context, item ItemInfo) error {
	detail, err := n.client.GetItemDetailWithRetry(ctx, item.ID)
	if err != nil || detail == nil {
		logger.S().Warnf("[Emby] 获取电影详情失败 id=%s: %v", item.ID, err)
		// 降级为简版通知（统一走 FormatMessage）
		metadata := map[string]string{
			"入库时间": formatNow(),
			"备注":   "详情获取失败，已降级为简版通知",
		}
		msg := notify.FormatMessage("📚 Emby 电影入库通知", orDefault(item.Name, "未知"), metadata)
		return n.dispatcher.Notify(ctx, msg)
	}

	msg := FormatMovieNotification(detail, "library.new")
	photoURL := n.client.BuildImageURLIfAvailable(item.ID, detail.ImageTags, ImageMaxWidth)
	if photoURL != "" {
		if err := n.dispatcher.NotifyWithPhoto(ctx, msg, photoURL); err != nil {
			// 图片发送失败降级纯文本
			logger.S().Warnf("[Emby] 图片通知失败，降级纯文本: %v", err)
			return n.dispatcher.Notify(ctx, msg)
		}
		return nil
	}
	return n.dispatcher.Notify(ctx, msg)
}

// handleSeriesEpisodeAdded 缓冲剧集入库（对齐 TS handleSeriesEpisodeAdded）
func (n *Notifier) handleSeriesEpisodeAdded(ctx context.Context, item ItemInfo) {
	if item.SeriesID == "" {
		return
	}
	seriesID := item.SeriesID

	n.addedMu.Lock()
	defer n.addedMu.Unlock()

	// 获取详情用于通知（同步获取，失败则用 ItemInfo 兜底）
	detail, err := n.client.GetItemDetailWithRetry(ctx, item.ID)
	if err != nil || detail == nil {
		detail = &ItemDetail{
			ID:                item.ID,
			Name:              item.Name,
			Type:              item.Type,
			SeriesName:        item.SeriesName,
			ParentIndexNumber: item.ParentIndexNumber,
			IndexNumber:       item.IndexNumber,
		}
	}

	buf, ok := n.addedBuffer[seriesID]
	if !ok {
		buf = &episodeBuffer{
			seriesID:   seriesID,
			seriesName: orDefault(item.SeriesName, item.Name, "未知"),
		}
		n.addedBuffer[seriesID] = buf
	}
	buf.episodes = append(buf.episodes, *detail)
	buf.seriesName = orDefault(item.SeriesName, buf.seriesName)
	buf.lastUpdated = time.Now()

	// 重置定时器
	if old := n.addedTimers[seriesID]; old != nil {
		old.Stop()
	}
	timer := time.AfterFunc(EpisodeDebounceWindow, func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
		defer cancel()
		n.flushAddedEpisodeBuffer(flushCtx, seriesID)
	})
	n.addedTimers[seriesID] = timer
}

// flushAddedEpisodeBuffer 刷新入库缓冲（对齐 TS flushAddedEpisodeBuffer）
func (n *Notifier) flushAddedEpisodeBuffer(ctx context.Context, seriesID string) {
	n.addedMu.Lock()
	timer := n.addedTimers[seriesID]
	if timer != nil {
		timer.Stop()
		delete(n.addedTimers, seriesID)
	}
	buf, ok := n.addedBuffer[seriesID]
	if !ok {
		n.addedMu.Unlock()
		return
	}
	delete(n.addedBuffer, seriesID)
	n.addedMu.Unlock()

	// 安全检查：缓冲距上次更新不足防抖窗口则跳过
	if time.Since(buf.lastUpdated) < EpisodeDebounceWindow-500*time.Millisecond {
		return
	}

	settings := n.settingsFn()
	if !settings.NotifyMediaAdded {
		return
	}

	// 尝试获取剧集详情（用于海报、简介、评分、导演、主演等完整元数据）
	var seriesDetail *ItemDetail
	if d, err := n.client.GetItemDetailWithRetry(ctx, seriesID); err == nil && d != nil {
		seriesDetail = d
	}

	var msg string
	if seriesDetail != nil {
		// 用剧集级完整元数据构造通知（QMS 风格：评分/主演/入库时间都从 seriesDetail 取）
		msg = FormatSeriesNotification(seriesDetail, buf.episodes, "library.new")
	} else {
		// 降级为简版通知（统一走 FormatMessage）
		content := buf.seriesName
		seasonEps := formatSeasonEpisodes(buf.episodes)
		if seasonEps != "" {
			content += "\n入库季集：" + seasonEps
		}
		metadata := map[string]string{
			"入库时间": formatNow(),
		}
		msg = notify.FormatMessage("📚 Emby 剧集入库通知", content, metadata)
	}

	// 优先带海报发送（优先 Backdrop 背景图）
	if seriesDetail != nil {
		photoURL := n.client.BuildImageURLIfAvailable(seriesID, seriesDetail.ImageTags, ImageMaxWidth)
		if photoURL != "" {
			if err := n.dispatcher.NotifyWithPhoto(ctx, msg, photoURL); err != nil {
				logger.S().Warnf("[Emby] 剧集图片通知失败，降级纯文本: %v", err)
				_ = n.dispatcher.Notify(ctx, msg)
			}
			return
		}
	}
	_ = n.dispatcher.Notify(ctx, msg)
}

// ==================== 删除通知 ====================

// handleMediaDeleted 处理删除通知
func (n *Notifier) handleMediaDeleted(ctx context.Context, item ItemInfo) error {
	settings := n.settingsFn()
	if !settings.NotifyMediaRemoved {
		return nil
	}
	switch item.Type {
	case "Movie":
		return n.handleMovieDeleted(ctx, item)
	case "Episode", "Series", "Season":
		n.handleSeriesEpisodeDeleted(ctx, item)
		return nil
	default:
		return nil
	}
}

// handleMovieDeleted 处理电影删除（对齐 TS handleMovieDeleted）
func (n *Notifier) handleMovieDeleted(ctx context.Context, item ItemInfo) error {
	detail := &ItemDetail{
		ID:             item.ID,
		Name:           item.Name,
		Type:           item.Type,
		ProductionYear: 0, // ItemInfo 里没有年份，不显示
	}
	msg := FormatDeletedMovieNotification(detail)
	return n.dispatcher.Notify(ctx, msg)
}

// handleSeriesEpisodeDeleted 缓冲剧集删除（对齐 TS handleSeriesEpisodeDeleted）
func (n *Notifier) handleSeriesEpisodeDeleted(ctx context.Context, item ItemInfo) {
	// P13修复：纯 Series/Season 项没有 SeriesId，走直接通知（不走防抖聚合）
	if item.SeriesID == "" {
		if item.Type == "Series" || item.Type == "Season" {
			settings := n.settingsFn()
			if !settings.NotifyMediaRemoved {
				return
			}
			typeLabel := "整剧"
			if item.Type == "Season" {
				typeLabel = "季"
			}
			content := fmt.Sprintf("%s名称：%s", typeLabel, orDefault(item.Name, "未知"))
			metadata := map[string]string{
				"删除时间": formatNow(),
			}
			msg := notify.FormatMessage("🗑️ Emby 媒体删除通知", content, metadata)
			_ = n.dispatcher.Notify(ctx, msg)
		}
		return
	}

	seriesID := item.SeriesID

	n.deletedMu.Lock()
	defer n.deletedMu.Unlock()

	detail := &ItemDetail{
		ID:                item.ID,
		Name:              item.Name,
		Type:              item.Type,
		SeriesName:        item.SeriesName,
		ParentIndexNumber: item.ParentIndexNumber,
		IndexNumber:       item.IndexNumber,
	}

	buf, ok := n.deletedBuffer[seriesID]
	if !ok {
		buf = &episodeBuffer{
			seriesID:   seriesID,
			seriesName: orDefault(item.SeriesName, item.Name, "未知"),
		}
		n.deletedBuffer[seriesID] = buf
	}
	buf.episodes = append(buf.episodes, *detail)
	buf.seriesName = orDefault(item.SeriesName, buf.seriesName)
	buf.lastUpdated = time.Now()

	// 重置定时器
	if old := n.deletedTimers[seriesID]; old != nil {
		old.Stop()
	}
	timer := time.AfterFunc(EpisodeDebounceWindow, func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
		defer cancel()
		n.flushDeletedEpisodeBuffer(flushCtx, seriesID)
	})
	n.deletedTimers[seriesID] = timer
}

// flushDeletedEpisodeBuffer 刷新删除缓冲
func (n *Notifier) flushDeletedEpisodeBuffer(ctx context.Context, seriesID string) {
	n.deletedMu.Lock()
	timer := n.deletedTimers[seriesID]
	if timer != nil {
		timer.Stop()
		delete(n.deletedTimers, seriesID)
	}
	buf, ok := n.deletedBuffer[seriesID]
	if !ok {
		n.deletedMu.Unlock()
		return
	}
	delete(n.deletedBuffer, seriesID)
	n.deletedMu.Unlock()

	// 安全检查：缓冲距上次更新不足防抖窗口则跳过
	if time.Since(buf.lastUpdated) < EpisodeDebounceWindow-500*time.Millisecond {
		return
	}

	settings := n.settingsFn()
	if !settings.NotifyMediaRemoved {
		return
	}

	// 用缓冲中的剧集列表格式化删除通知（显示具体删了哪些季集，对齐 qmediasync）
	detail := &ItemDetail{
		Name:       buf.seriesName,
		SeriesName: buf.seriesName,
	}
	msg := FormatDeletedSeriesNotification(detail, buf.episodes)
	_ = n.dispatcher.Notify(ctx, msg)
}

// ==================== 播放通知 ====================

// handlePlaybackEvent 处理播放事件（对齐 qmediasync 方案）
// 核心优化：
//   1. 播放进度直接从 Webhook.PlaybackInfo.MediaSource 获取，无需请求详情
//   2. 图片优先使用 Webhook.Item.ImageTags，避免额外 API 调用
//   3. 仅当 showOverview=true 时才请求详情（简介不在 Webhook 中）
func (n *Notifier) handlePlaybackEvent(ctx context.Context, event WebhookEvent) error {
	settings := n.settingsFn()
	if !settings.NotifyPlayback {
		return nil
	}

	// 去重（60 秒内不重复，含设备信息避免不同设备误去重）
	cacheKey := n.buildPlaybackCacheKey(event)
	if n.isPlaybackDuplicate(cacheKey) {
		logger.S().Infof("[Emby] 播放事件 60s 内重复，跳过: %s", cacheKey)
		return nil
	}

	showProgress := settings.PlaybackShowProgress
	showOverview := settings.PlaybackShowOverview

	// 1. 从 Webhook 直接获取播放数据（对齐 qmediasync）
	var positionTicks, runtimeTicks int64
	if event.PlaybackInfo != nil {
		positionTicks = event.PlaybackInfo.PositionTicks
		if event.PlaybackInfo.MediaSource != nil {
			runtimeTicks = event.PlaybackInfo.MediaSource.RunTimeTicks
		}
	}

	// 2. 构造基础 item 信息（无需请求详情）
	item := &ItemDetail{}
	if event.Item != nil {
		item.ID = event.Item.ID
		item.Name = event.Item.Name
		item.Type = event.Item.Type
		item.SeriesName = event.Item.SeriesName
		item.ParentIndexNumber = event.Item.ParentIndexNumber
		item.IndexNumber = event.Item.IndexNumber
		item.ImageTags = event.Item.ImageTags // 直接使用 Webhook 自带的图片标签
	}
	item.RunTimeTicks = runtimeTicks // 从 Webhook 获取，无需详情

	var user *UserInfo
	if event.User != nil {
		user = event.User
	}

	// 3. 仅当需要简介时才请求详情（简介不在 Webhook 中）
	if showOverview && event.Item != nil && event.Item.ID != "" {
		if detail, err := n.client.GetItemDetailWithRetry(ctx, event.Item.ID); err == nil && detail != nil {
			item.Overview = detail.Overview
			item.ProductionYear = detail.ProductionYear
			item.CommunityRating = detail.CommunityRating
			item.Genres = detail.Genres
			item.People = detail.People
			// 详情中的 ImageTags 更完整，优先使用
			if len(detail.ImageTags) > 0 {
				item.ImageTags = detail.ImageTags
			}
		}
	}

	// 4. 格式化通知
	msg := FormatPlaybackNotification(
		event.Event,
		item,
		user,
		event.DeviceName,
		event.Client,
		positionTicks,
		showProgress,
		showOverview,
	)

	// 5. 带海报发送（优先使用 Webhook/详情中的 ImageTags）
	if item.ID != "" {
		photoURL := n.client.BuildPrimaryImageURL(item.ID, item.ImageTags, ImageMaxWidth)
		if photoURL != "" {
			if err := n.dispatcher.NotifyWithPhoto(ctx, msg, photoURL); err != nil {
				logger.S().Warnf("[Emby] 播放图片通知失败，降级纯文本: %v", err)
				return n.dispatcher.Notify(ctx, msg)
			}
			return nil
		}
	}
	return n.dispatcher.Notify(ctx, msg)
}

// buildPlaybackCacheKey 构建播放事件去重 key（含设备信息，避免不同设备误去重）
func (n *Notifier) buildPlaybackCacheKey(event WebhookEvent) string {
	var userID, itemType, itemName string
	if event.User != nil {
		userID = event.User.ID
	}
	if event.Item != nil {
		itemType = event.Item.Type
		itemName = event.Item.Name
	}
	// 设备信息也加入 key，避免同一用户不同设备触发被去重
	return fmt.Sprintf("%s_%s_%s_%s_%s_%s",
		userID, itemType, itemName, event.Event, event.DeviceName, event.Client)
}

// isPlaybackDuplicate 检查播放事件是否在去重窗口内重复
func (n *Notifier) isPlaybackDuplicate(cacheKey string) bool {
	n.playbackMu.Lock()
	defer n.playbackMu.Unlock()

	now := time.Now()
	// 清理过期条目
	for k, ts := range n.playbackCache {
		if now.Sub(ts) > PlaybackCacheTTL {
			delete(n.playbackCache, k)
		}
	}

	if ts, ok := n.playbackCache[cacheKey]; ok {
		if now.Sub(ts) < PlaybackDedupWindow {
			return true
		}
	}
	n.playbackCache[cacheKey] = now
	return false
}

// ==================== 通知模板（对齐 qmediasync 风格） ====================

// qmediasyncNotificationTemplate 对齐 qmediasync 通知模板风格
// 格式: emoji前缀 + 半角冒号 + 简介独立段落
const qmediasyncNotificationTemplate = `%s

🆔 评分: %s
🎬 类型: %s
👤 主演: %s
⏰ 入库时间: %s

📝 简介
%s`

// FormatMovieNotification 格式化电影入库通知（qmediasync 风格模板）
func FormatMovieNotification(item *ItemDetail, eventType string) string {
	if item == nil {
		return ""
	}
	genres := "暂无数据"
	if len(item.Genres) > 0 {
		genres = strings.Join(item.Genres, ", ")
	}
	actors := extractActors(item.People, 5)
	overview := orDefault(item.Overview, "暂无简介")
	rating := "暂无数据"
	if item.CommunityRating > 0 {
		rating = strconv.FormatFloat(item.CommunityRating, 'f', 1, 64)
	}
	addedTime := formatDateCreated(item.DateCreated)

	title := orDefault(item.Name, "未知")
	if item.ProductionYear > 0 {
		title = fmt.Sprintf("%s (%d)", title, item.ProductionYear)
	}

	runes := []rune(overview)
	if len(runes) > 100 {
		overview = string(runes[:100]) + "..."
	}

	return fmt.Sprintf(qmediasyncNotificationTemplate,
		title, rating, genres, actors, addedTime, overview)
}

// FormatSeriesNotification 格式化电视剧入库通知（qmediasync 风格模板）
func FormatSeriesNotification(seriesDetail *ItemDetail, episodes []ItemDetail, eventType string) string {
	if seriesDetail == nil {
		return ""
	}
	seasonEpisodesStr := formatSeasonEpisodes(episodes)

	// 元数据优先从 seriesDetail 取（剧集级信息最准确），缺失才从 episodes 兜底
	overview := orDefault(seriesDetail.Overview, "暂无简介")
	if overview == "暂无简介" {
		for _, ep := range episodes {
			if ep.Overview != "" {
				overview = ep.Overview
				break
			}
		}
	}
	runes := []rune(overview)
	if len(runes) > 100 {
		overview = string(runes[:100]) + "..."
	}

	genres := "暂无数据"
	if len(seriesDetail.Genres) > 0 {
		genres = strings.Join(seriesDetail.Genres, ", ")
	} else {
		for _, ep := range episodes {
			if len(ep.Genres) > 0 {
				genres = strings.Join(ep.Genres, ", ")
				break
			}
		}
	}

	title := orDefault(seriesDetail.Name, "未知")
	if seriesDetail.ProductionYear > 0 {
		title = fmt.Sprintf("%s (%d)", title, seriesDetail.ProductionYear)
	} else {
		for _, ep := range episodes {
			if ep.ProductionYear > 0 {
				title = fmt.Sprintf("%s (%d)", orDefault(seriesDetail.Name, "未知"), ep.ProductionYear)
				break
			}
		}
	}

	rating := "暂无数据"
	if seriesDetail.CommunityRating > 0 {
		rating = strconv.FormatFloat(seriesDetail.CommunityRating, 'f', 1, 64)
	} else {
		for _, ep := range episodes {
			if ep.CommunityRating > 0 {
				rating = strconv.FormatFloat(ep.CommunityRating, 'f', 1, 64)
				break
			}
		}
	}

	actors := extractActors(seriesDetail.People, 5)
	addedTime := formatDateCreated(seriesDetail.DateCreated)

	// 格式化通知，然后将季集信息插入到入库时间之前（对齐 qmediasync）
	content := fmt.Sprintf(qmediasyncNotificationTemplate,
		title, rating, genres, actors, addedTime, overview)

	if seasonEpisodesStr != "" {
		seasonLine := fmt.Sprintf("📺 入库季集: %s\n", seasonEpisodesStr)
		content = strings.ReplaceAll(content, "⏰ 入库时间:", seasonLine+"⏰ 入库时间:")
	}

	return content
}

// FormatDeletedMovieNotification 格式化电影删除通知（统一走 FormatMessage 三段式渲染）
func FormatDeletedMovieNotification(item *ItemDetail) string {
	if item == nil {
		return ""
	}
	year := ""
	if item.ProductionYear > 0 {
		year = fmt.Sprintf(" (%d)", item.ProductionYear)
	}
	content := fmt.Sprintf("电影名称：%s%s", orDefault(item.Name, "未知"), year)
	metadata := map[string]string{
		"删除时间": formatNow(),
	}
	return notify.FormatMessage("🗑️ Emby 媒体删除通知", content, metadata)
}

// FormatDeletedSeriesNotification 格式化电视剧删除通知（episodes 传入可显示具体删了哪些季集）
// 统一走 FormatMessage 三段式渲染
func FormatDeletedSeriesNotification(item *ItemDetail, episodes []ItemDetail) string {
	if item == nil {
		return ""
	}
	name := orDefault(item.Name, "未知")
	if item.SeriesName != "" {
		name = item.SeriesName
	}
	content := fmt.Sprintf("电视剧名称：%s", name)
	seasonEpisodesStr := formatSeasonEpisodes(episodes)
	if seasonEpisodesStr != "" {
		content += "\n删除季集：" + seasonEpisodesStr
	}
	metadata := map[string]string{
		"删除时间": formatNow(),
	}
	return notify.FormatMessage("🗑️ Emby 媒体删除通知", content, metadata)
}

// FormatPlaybackNotification 格式化播放通知（qmediasync 风格）
// emoji 前缀 + 半角冒号 + 无 HTML 标签，与入库通知风格统一
// showProgress/showOverview 由调用方从 EmbySettings 传入，关闭时不显示对应字段
func FormatPlaybackNotification(event string, item *ItemDetail, user *UserInfo, deviceName, client string, positionTicks int64, showProgress, showOverview bool) string {
	if item == nil {
		return ""
	}

	var sb strings.Builder

	// 标题行：emoji + 事件名 + 片名
	title := orDefault(item.Name, "未知")
	fmt.Fprintf(&sb, "%s %s %s\n", GetEventTypeEmoji(event), GetEventTypeName(event), title)

	// 用户信息
	userName := "未知"
	if user != nil && user.Name != "" {
		userName = user.Name
	}
	fmt.Fprintf(&sb, "👤 用户: %s\n", userName)

	// 设备信息
	if deviceName != "" || client != "" {
		if deviceName != "" && client != "" {
			fmt.Fprintf(&sb, "📱 设备: %s (%s)\n", deviceName, client)
		} else if deviceName != "" {
			fmt.Fprintf(&sb, "📱 设备: %s\n", deviceName)
		} else {
			fmt.Fprintf(&sb, "📱 设备: %s\n", client)
		}
	}

	// 剧集信息
	if item.Type == "Episode" {
		if item.SeriesName != "" {
			fmt.Fprintf(&sb, "📺 电视剧: %s\n", item.SeriesName)
		}
		if item.ParentIndexNumber > 0 && item.IndexNumber > 0 {
			fmt.Fprintf(&sb, "🎬 季集: S%dE%d\n", item.ParentIndexNumber, item.IndexNumber)
		}
	}

	// 观看时长（暂停/停止事件）
	if (event == "playback.pause" || event == "playback.stop") && positionTicks > 0 {
		fmt.Fprintf(&sb, "⏱️ 观看时长: %s\n", formatWatchedDuration(positionTicks))
	}

	// 播放进度
	if showProgress && positionTicks > 0 && item.RunTimeTicks > 0 {
		positionStr := FormatTicksToTime(positionTicks)
		runtimeStr := FormatTicksToTime(item.RunTimeTicks)
		percentage := float64(positionTicks) / float64(item.RunTimeTicks) * 100
		fmt.Fprintf(&sb, "📊 播放进度: %s / %s (%.0f%%)\n", positionStr, runtimeStr, percentage)
	} else if showProgress && item.RunTimeTicks > 0 {
		fmt.Fprintf(&sb, "⏱️ 时长: %s\n", FormatTicksToTime(item.RunTimeTicks))
	}

	// 剧情简介
	if showOverview && item.Overview != "" {
		overview := item.Overview
		runes := []rune(overview)
		if len(runes) > 100 {
			overview = string(runes[:100]) + "..."
		}
		fmt.Fprintf(&sb, "\n📝 简介\n%s", overview)
	}

	return sb.String()
}

// FormatTicksToTime 将 Emby ticks（100ns 单位）转为 HH:MM:SS 或 MM:SS
func FormatTicksToTime(ticks int64) string {
	// Emby ticks: 1 tick = 10,000 nanoseconds = 0.00001 seconds
	totalSeconds := ticks / 10_000_000
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d", minutes, seconds)
}

// formatWatchedDuration 把 ticks 转为中文"观看时长"描述（qmediasync 风格："12分钟"/"1小时5分钟"）
func formatWatchedDuration(ticks int64) string {
	totalSeconds := ticks / 10_000_000
	// 不到 1 分钟按 1 分钟显示，避免"0分钟"
	if totalSeconds < 60 {
		return "1分钟"
	}
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	if hours > 0 {
		return fmt.Sprintf("%d小时%d分钟", hours, minutes)
	}
	return fmt.Sprintf("%d分钟", minutes)
}

// GetEventTypeEmoji 根据事件类型返回对应 emoji
// 📚 入库 / 🗑 删除 / 📺 播放 / ⏸ 暂停 / ⛔ 停止
func GetEventTypeEmoji(eventType string) string {
	switch eventType {
	case "library.new":
		return "📚"
	case "library.deleted":
		return "🗑️"
	case "playback.start":
		return "📺"
	case "playback.pause":
		return "⏸️"
	case "playback.stop":
		return "⏹️"
	default:
		return "📺"
	}
}

// GetEventTypeName 根据事件类型返回中文名称
func GetEventTypeName(eventType string) string {
	switch eventType {
	case "library.new":
		return "媒体入库"
	case "library.deleted":
		return "媒体删除"
	case "playback.start":
		return "播放开始"
	case "playback.pause":
		return "播放暂停"
	case "playback.stop":
		return "播放停止"
	default:
		return "播放事件"
	}
}

// ==================== 人物提取辅助 ====================

// extractActors 从 People 中提取前 max 个 Actor 类型的人物，逗号分隔；max <= 0 返回全部
func extractActors(people []Person, max int) string {
	if len(people) == 0 {
		return "暂无数据"
	}
	var names []string
	count := 0
	for _, p := range people {
		if strings.EqualFold(p.Type, "Actor") {
			names = append(names, p.Name)
			count++
			if max > 0 && count >= max {
				break
			}
		}
	}
	if len(names) == 0 {
		return "暂无数据"
	}
	return strings.Join(names, ", ")
}

// ==================== 内部辅助 ====================

// formatSeasonEpisodes 从剧集列表构建季集分组并格式化（对齐 TS formatSeasonEpisodes）
// 输出示例：S1E1-E3,E5; S2E1,E2
func formatSeasonEpisodes(episodes []ItemDetail) string {
	// 构建 season -> []episode map
	seasons := make(map[int][]int)
	for _, ep := range episodes {
		episode := ep.IndexNumber
		if episode == 0 {
			continue
		}
		season := ep.ParentIndexNumber
		seasons[season] = append(seasons[season], episode)
	}
	if len(seasons) == 0 {
		return ""
	}

	// 排序季号
	var seasonNums []int
	for s := range seasons {
		seasonNums = append(seasonNums, s)
	}
	sort.Ints(seasonNums)

	var parts []string
	for _, season := range seasonNums {
		eps := seasons[season]
		if len(eps) == 0 {
			continue
		}
		// 排序 + 去重
		sort.Ints(eps)
		unique := eps[:0]
		for i, e := range eps {
			if i == 0 || e != eps[i-1] {
				unique = append(unique, e)
			}
		}
		if len(unique) == 0 {
			continue
		}

		// 格式化连续区间（连续 E1-E3，不连续 E1,E3,E5）
		var sb strings.Builder
		start := unique[0]
		prev := unique[0]
		for i := 1; i < len(unique); i++ {
			if unique[i] != prev+1 {
				if start == prev {
					sb.WriteString(fmt.Sprintf("E%d,", start))
				} else {
					sb.WriteString(fmt.Sprintf("E%d-E%d,", start, prev))
				}
				start = unique[i]
			}
			prev = unique[i]
		}
		if start == prev {
			sb.WriteString(fmt.Sprintf("E%d", start))
		} else {
			sb.WriteString(fmt.Sprintf("E%d-E%d", start, prev))
		}
		parts = append(parts, fmt.Sprintf("S%d%s", season, sb.String()))
	}
	// 替换成 "S1E1-E3, E5" 风格（逗号后加空格更美观）
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(p, ",", ", ")
	}
	return strings.Join(parts, "; ")
}

// formatDateCreated 解析 Emby DateCreated，失败回退当前时间
func formatDateCreated(dateCreated string) string {
	if dateCreated == "" {
		return formatNow()
	}
	// 尝试 RFC3339 解析
	t, err := time.Parse(time.RFC3339, dateCreated)
	if err != nil {
		// 尝试其他常见格式
		t, err = time.Parse("2006-01-02T15:04:05", dateCreated)
	}
	if err != nil {
		return formatNow()
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// formatNow 返回当前时间的本地格式化字符串
func formatNow() string {
	return time.Now().Local().Format("2006-01-02 15:04:05")
}

// orDefault 返回第一个非空字符串，全空则返回 fallback
func orDefault(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
