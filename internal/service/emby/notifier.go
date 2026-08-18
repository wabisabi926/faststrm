// Emby Webhook 事件分发器 + 通知模板
// 对齐 frontend/src/lib/emby/notifierDispatcher.ts 和 notifierTemplates.ts
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
	// ImageMaxWidth 通知图片默认最大宽度
	ImageMaxWidth = 400
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

	// 防御：Emby 未配置时 client 为 nil，直接跳过详情/图片相关处理
	if n.client == nil {
		logger.S().Warnf("[Emby] EmbyClient 未初始化，跳过事件 %s 处理", event.Event)
		return nil
	}

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
	detail, err := n.client.GetItemDetail(ctx, item.ID)
	if err != nil || detail == nil {
		logger.S().Warnf("[Emby] 获取电影详情失败 id=%s: %v", item.ID, err)
		// 降级为简版通知
		msg := fmt.Sprintf("<b>📺 Emby 电影入库通知</b>\n\n<b>%s</b>\n\n⏰ 入库时间: %s\n\n<i>（详情获取失败，已降级为简版通知）</i>",
			orDefault(item.Name, "未知"), formatNow())
		return n.dispatcher.Notify(ctx, msg)
	}

	msg := FormatMovieNotification(detail, "library.new")
	photoURL := n.client.BuildImageURL(item.ID, ImageMaxWidth)
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
	detail, err := n.client.GetItemDetail(ctx, item.ID)
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

	// 尝试获取剧集详情（用于海报和简介）
	var seriesDetail *ItemDetail
	if d, err := n.client.GetItemDetail(ctx, seriesID); err == nil && d != nil {
		seriesDetail = d
	}

	var msg string
	if seriesDetail != nil {
		msg = FormatSeriesNotification(seriesDetail.Name, buf.episodes, "library.new")
	} else {
		msg = fmt.Sprintf("<b>📺 Emby 电视剧入库通知</b>\n\n<b>%s</b>\n📺 入库季集: %s\n⏰ 入库时间: %s",
			buf.seriesName, formatSeasonEpisodes(buf.episodes), formatNow())
	}

	// 优先带海报发送
	if seriesDetail != nil {
		photoURL := n.client.BuildImageURL(seriesID, ImageMaxWidth)
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
		ID:   item.ID,
		Name: item.Name,
		Type: item.Type,
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
			msg := fmt.Sprintf("🗑️ <b>%s已删除</b>\n<b>标题:</b> %s", typeLabel, orDefault(item.Name, "未知"))
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

// flushDeletedEpisodeBuffer 刷新删除缓冲（对齐 TS flushDeletedEpisodeBuffer）
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

	// 用缓冲中的剧集列表格式化删除通知
	detail := &ItemDetail{
		Name:       buf.seriesName,
		SeriesName: buf.seriesName,
	}
	msg := FormatDeletedSeriesNotification(detail)
	_ = n.dispatcher.Notify(ctx, msg)
}

// ==================== 播放通知 ====================

// handlePlaybackEvent 处理播放事件（对齐 TS handlePlaybackEvent，60s 去重）
func (n *Notifier) handlePlaybackEvent(ctx context.Context, event WebhookEvent) error {
	settings := n.settingsFn()
	if !settings.NotifyPlayback {
		return nil
	}

	// 去重（60 秒内不重复）
	cacheKey := n.buildPlaybackCacheKey(event)
	if n.isPlaybackDuplicate(cacheKey) {
		logger.S().Infof("[Emby] 播放事件 60s 内重复，跳过: %s", cacheKey)
		return nil
	}

	// 获取详情（用于简介和时长）
	var detail *ItemDetail
	if event.Item != nil && event.Item.ID != "" {
		if d, err := n.client.GetItemDetail(ctx, event.Item.ID); err == nil && d != nil {
			detail = d
		}
	}
	if detail == nil && event.Item != nil {
		detail = &ItemDetail{
			ID:                event.Item.ID,
			Name:              event.Item.Name,
			Type:              event.Item.Type,
			SeriesName:        event.Item.SeriesName,
			ParentIndexNumber: event.Item.ParentIndexNumber,
			IndexNumber:       event.Item.IndexNumber,
		}
	}

	var user *UserInfo
	if event.User != nil {
		user = event.User
	}

	msg := FormatPlaybackNotification(event.Event, detail, user)

	// 带海报发送
	if event.Item != nil && event.Item.ID != "" {
		photoURL := n.client.BuildImageURL(event.Item.ID, ImageMaxWidth)
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

// buildPlaybackCacheKey 构建播放事件去重 key
func (n *Notifier) buildPlaybackCacheKey(event WebhookEvent) string {
	var userID, itemType, itemName string
	if event.User != nil {
		userID = event.User.ID
	}
	if event.Item != nil {
		itemType = event.Item.Type
		itemName = event.Item.Name
	}
	return fmt.Sprintf("%s_%s_%s_%s", userID, itemType, itemName, event.Event)
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

// ==================== 通知模板（对齐 TS notifierTemplates.ts） ====================

// FormatMovieNotification 格式化电影通知
func FormatMovieNotification(item *ItemDetail, eventType string) string {
	if item == nil {
		return ""
	}
	genres := "暂无数据"
	if len(item.Genres) > 0 {
		genres = strings.Join(item.Genres, ", ")
	}
	actors := "暂无数据"
	if len(item.People) > 0 {
		var names []string
		for _, p := range item.People {
			if p.Type == "Actor" {
				names = append(names, p.Name)
				if len(names) >= 5 {
					break
				}
			}
		}
		if len(names) > 0 {
			actors = strings.Join(names, ", ")
		}
	}
	overview := orDefault(item.Overview, "暂无简介")
	rating := "暂无数据"
	if item.CommunityRating > 0 {
		rating = strconv.FormatFloat(item.CommunityRating, 'f', 1, 64)
	}
	addedTime := formatDateCreated(item.DateCreated)
	year := "未知"
	if item.ProductionYear > 0 {
		year = strconv.Itoa(item.ProductionYear)
	}

	return strings.TrimSpace(fmt.Sprintf(`%s <b>Emby 电影入库通知</b>

<b>%s</b> (%s)

🆔 评分: %s
🎬 类型: %s
👤 主演: %s
⏰ 入库时间: %s

📝 简介
%s`,
		GetEventTypeEmoji(eventType),
		orDefault(item.Name, "未知"),
		year,
		rating,
		genres,
		actors,
		addedTime,
		overview,
	))
}

// FormatSeriesNotification 格式化电视剧通知
func FormatSeriesNotification(series string, episodes []ItemDetail, eventType string) string {
	seasonEpisodesStr := formatSeasonEpisodes(episodes)
	seasonEpisodesLine := ""
	if seasonEpisodesStr != "" {
		seasonEpisodesLine = fmt.Sprintf("📺 入库季集: %s\n", seasonEpisodesStr)
	}

	// 从 episodes 中提取 overview（取第一个非空的）
	overview := "暂无简介"
	for _, ep := range episodes {
		if ep.Overview != "" {
			overview = ep.Overview
			break
		}
	}

	// 从 episodes 中提取 genres
	genres := "暂无数据"
	for _, ep := range episodes {
		if len(ep.Genres) > 0 {
			genres = strings.Join(ep.Genres, ", ")
			break
		}
	}

	// 年份
	year := "未知"
	for _, ep := range episodes {
		if ep.ProductionYear > 0 {
			year = strconv.Itoa(ep.ProductionYear)
			break
		}
	}

	return strings.TrimSpace(fmt.Sprintf(`%s <b>Emby 电视剧入库通知</b>

<b>%s</b> (%s)
%s🆔 评分: 暂无数据
🎬 类型: %s
⏰ 入库时间: %s

📝 简介
%s`,
		GetEventTypeEmoji(eventType),
		orDefault(series, "未知"),
		year,
		seasonEpisodesLine,
		genres,
		formatNow(),
		overview,
	))
}

// FormatDeletedMovieNotification 格式化电影删除通知
func FormatDeletedMovieNotification(item *ItemDetail) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf(`🗑️ <b>Emby 媒体删除通知</b>

<b>电影名称：</b>%s
⏰ 删除时间: %s`,
		orDefault(item.Name, "未知"),
		formatNow(),
	))
}

// FormatDeletedSeriesNotification 格式化电视剧删除通知
func FormatDeletedSeriesNotification(item *ItemDetail) string {
	if item == nil {
		return ""
	}
	name := orDefault(item.Name, "未知")
	if item.SeriesName != "" {
		name = item.SeriesName
	}
	return strings.TrimSpace(fmt.Sprintf(`🗑️ <b>Emby 媒体删除通知</b>

<b>电视剧名称：</b>%s
⏰ 删除时间: %s`,
		name,
		formatNow(),
	))
}

// FormatPlaybackNotification 格式化播放通知
func FormatPlaybackNotification(event string, item *ItemDetail, user *UserInfo) string {
	if item == nil {
		return ""
	}
	titleLine := fmt.Sprintf("%s <b>%s %s</b>\n",
		GetEventTypeEmoji(event),
		GetEventTypeName(event),
		orDefault(item.Name, "未知"),
	)

	var sb strings.Builder
	sb.WriteString(titleLine)

	userName := "未知"
	if user != nil && user.Name != "" {
		userName = user.Name
	}
	sb.WriteString(fmt.Sprintf("👤 用户: %s\n", userName))

	if item.Type == "Episode" {
		if item.SeriesName != "" {
			sb.WriteString(fmt.Sprintf("📺 电视剧: %s\n", item.SeriesName))
		}
		if item.ParentIndexNumber > 0 && item.IndexNumber > 0 {
			sb.WriteString(fmt.Sprintf("👟 季集: S%dE%d\n", item.ParentIndexNumber, item.IndexNumber))
		}
	}

	// 时长
	if item.RunTimeTicks > 0 {
		sb.WriteString(fmt.Sprintf("⏱️ 时长: %s\n", FormatTicksToTime(item.RunTimeTicks)))
	}

	// 播放结束：无额外观看时长信息（WebhookEvent 不含 PlaybackInfo）
	// 简介
	if item.Overview != "" {
		overview := item.Overview
		// 截断长简介
		runes := []rune(overview)
		if len(runes) > 100 {
			overview = string(runes[:100]) + "..."
		}
		sb.WriteString(fmt.Sprintf("📝 简介: %s\n", overview))
	}

	return strings.TrimRight(sb.String(), "\n")
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

// GetEventTypeEmoji 根据事件类型返回对应 emoji
// 🎬 入库 / 🗑 删除 / ▶️ 播放 / ⏸ 暂停 / ⏹ 停止
func GetEventTypeEmoji(eventType string) string {
	switch eventType {
	case "library.new":
		return "🎬"
	case "library.deleted":
		return "🗑️"
	case "playback.start":
		return "▶️"
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
		return "播放结束"
	default:
		return "播放事件"
	}
}

// ==================== 内部辅助 ====================

// formatSeasonEpisodes 从剧集列表构建季集分组并格式化（对齐 TS formatSeasonEpisodes）
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

		// 格式化连续区间
		var sb strings.Builder
		start := unique[0]
		prev := unique[0]
		for i := 1; i < len(unique); i++ {
			if unique[i] != prev+1 {
				if start == prev {
					sb.WriteString(fmt.Sprintf("E%d, ", start))
				} else {
					sb.WriteString(fmt.Sprintf("E%d-E%d, ", start, prev))
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
