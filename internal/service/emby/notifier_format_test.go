package emby

import (
	"strings"
	"testing"
)

// TestFormatMovieNotification_NoDirector 验证电影入库通知不含导演字段（qmediasync 风格）
func TestFormatMovieNotification_NoDirector(t *testing.T) {
	item := &ItemDetail{
		Name:            "功夫熊猫",
		ProductionYear:  2008,
		CommunityRating: 8.2,
		Genres:          []string{"动画", "动作"},
		Overview:        "一只熊猫成为神龙大侠的故事",
		DateCreated:     "2026-01-01T00:00:00.000Z",
		People: []Person{
			{Name: "导演甲", Type: "Director"},
			{Name: "演员A", Type: "Actor"},
			{Name: "演员B", Type: "Actor"},
		},
	}
	out := FormatMovieNotification(item, "library.new")
	if out == "" {
		t.Fatal("输出不应为空")
	}
	if strings.Contains(out, "导演") {
		t.Errorf("要求去掉导演字段，但输出含导演: %s", out)
	}
	if !strings.HasPrefix(out, "功夫熊猫 (2008)") {
		t.Errorf("应以 功夫熊猫 (2008) 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "🆔 评分: 8.2") {
		t.Errorf("应含 🆔 评分: 8.2, 实际 %s", out)
	}
	if !strings.Contains(out, "🎬 类型: 动画, 动作") {
		t.Errorf("应含 🎬 类型: 动画, 动作, 实际 %s", out)
	}
	if !strings.Contains(out, "👤 主演: 演员A, 演员B") {
		t.Errorf("应含 👤 主演: 演员A, 演员B, 实际 %s", out)
	}
	if !strings.Contains(out, "⏰ 入库时间:") {
		t.Errorf("应含 ⏰ 入库时间:, 实际 %s", out)
	}
	if !strings.Contains(out, "📝 简介") {
		t.Errorf("应含 📝 简介 独立段落, 实际 %s", out)
	}
	if !strings.Contains(out, "一只熊猫成为神龙大侠的故事") {
		t.Errorf("应含简介内容, 实际 %s", out)
	}
}

// TestFormatMovieNotification_NilItem 验证 nil 时返回空串
func TestFormatMovieNotification_NilItem(t *testing.T) {
	if out := FormatMovieNotification(nil, "library.new"); out != "" {
		t.Errorf("nil item 应返回空串, 实际 %q", out)
	}
}

// TestFormatMovieNotification_OverviewTruncated 验证简介超长被截断为100字（对齐 qmediasync）
func TestFormatMovieNotification_OverviewTruncated(t *testing.T) {
	// 100字以上才会被截断
	long := strings.Repeat("一二三四五六七八九十", 11) // 110字
	item := &ItemDetail{
		Name:     "长简介电影",
		Overview: long,
		People:   []Person{},
	}
	out := FormatMovieNotification(item, "library.new")
	if !strings.Contains(out, "...") {
		t.Errorf("超长简介应被截断加 ..., 实际 %s", out)
	}
	// 验证截断位置在100字处
	expectedPrefix := long[:100]
	if !strings.Contains(out, expectedPrefix) {
		t.Errorf("应包含前100字, 实际 %s", out)
	}
}

// TestFormatMovieNotification_OverviewNotTruncated 验证100字以内简介不截断
func TestFormatMovieNotification_OverviewNotTruncated(t *testing.T) {
	short := strings.Repeat("一二三四五六七八九十", 5) // 50字
	item := &ItemDetail{
		Name:     "短简介电影",
		Overview: short,
		People:   []Person{},
	}
	out := FormatMovieNotification(item, "library.new")
	if strings.Contains(out, "...") {
		t.Errorf("100字以内简介不应被截断, 实际 %s", out)
	}
}

// TestFormatSeriesNotification_SeasonEpisodesPosition 验证季集信息插入入库时间之前（对齐 qmediasync）
func TestFormatSeriesNotification_SeasonEpisodesPosition(t *testing.T) {
	series := &ItemDetail{
		Name:           "测试剧集",
		ProductionYear: 2024,
		Genres:         []string{"剧情"},
		Overview:       "测试简介",
		DateCreated:    "2026-01-01T00:00:00.000Z",
		People:         []Person{},
	}
	episodes := []ItemDetail{
		{IndexNumber: 1, ParentIndexNumber: 1},
		{IndexNumber: 2, ParentIndexNumber: 1},
	}
	out := FormatSeriesNotification(series, episodes, "library.new")

	// 验证季集信息包含正确格式
	if !strings.Contains(out, "📺 入库季集:") {
		t.Errorf("应含 📺 入库季集:, 实际 %s", out)
	}

	// 验证季集信息在入库时间之前
	seasonIdx := strings.Index(out, "📺 入库季集:")
	timeIdx := strings.Index(out, "⏰ 入库时间:")
	if seasonIdx == -1 || timeIdx == -1 {
		t.Fatal("应包含季集和入库时间")
	}
	if seasonIdx > timeIdx {
		t.Errorf("季集信息应在入库时间之前, 实际顺序错误")
	}
}

// TestFormatSeriesNotification_NoEpisodes 验证无季集时不显示季集行
func TestFormatSeriesNotification_NoEpisodes(t *testing.T) {
	series := &ItemDetail{
		Name:           "测试剧集",
		ProductionYear: 2024,
		Genres:         []string{"剧情"},
		Overview:       "测试简介",
		DateCreated:    "2026-01-01T00:00:00.000Z",
		People:         []Person{},
	}
	out := FormatSeriesNotification(series, nil, "library.new")
	if strings.Contains(out, "📺 入库季集:") {
		t.Errorf("无季集时不应显示季集行, 实际 %s", out)
	}
}

// TestFormatSeriesNotification_NoDirector 验证剧集入库通知不含导演字段（qmediasync 风格）
func TestFormatSeriesNotification_NoDirector(t *testing.T) {
	series := &ItemDetail{
		Name:            "权力的游戏",
		ProductionYear:  2011,
		CommunityRating: 9.2,
		Genres:          []string{"剧情", "奇幻"},
		Overview:        "七大王国争霸",
		DateCreated:     "2026-01-01T00:00:00.000Z",
		People: []Person{
			{Name: "导演乙", Type: "Director"},
			{Name: "演员C", Type: "Actor"},
		},
	}
	episodes := []ItemDetail{
		{IndexNumber: 1, ParentIndexNumber: 1, Name: "凛冬将至"},
	}
	out := FormatSeriesNotification(series, episodes, "library.new")
	if out == "" {
		t.Fatal("输出不应为空")
	}
	if strings.Contains(out, "导演") {
		t.Errorf("要求去掉导演字段，但输出含导演: %s", out)
	}
	if !strings.HasPrefix(out, "权力的游戏 (2011)") {
		t.Errorf("应以 权力的游戏 (2011) 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "🆔 评分: 9.2") {
		t.Errorf("应含 🆔 评分: 9.2, 实际 %s", out)
	}
	if !strings.Contains(out, "🎬 类型: 剧情, 奇幻") {
		t.Errorf("应含 🎬 类型: 剧情, 奇幻, 实际 %s", out)
	}
	if !strings.Contains(out, "👤 主演: 演员C") {
		t.Errorf("应含 👤 主演: 演员C, 实际 %s", out)
	}
	if !strings.Contains(out, "⏰ 入库时间:") {
		t.Errorf("应含 ⏰ 入库时间:, 实际 %s", out)
	}
	if !strings.Contains(out, "📝 简介") {
		t.Errorf("应含 📝 简介 独立段落, 实际 %s", out)
	}
	if !strings.Contains(out, "七大王国争霸") {
		t.Errorf("应含简介内容, 实际 %s", out)
	}
}

// TestFormatDeletedMovieNotification_UsesFormatMessage 验证电影删除通知走 FormatMessage
func TestFormatDeletedMovieNotification_UsesFormatMessage(t *testing.T) {
	item := &ItemDetail{Name: "被删电影", ProductionYear: 2020}
	out := FormatDeletedMovieNotification(item)
	if !strings.HasPrefix(out, "<b>🗑️ Emby 媒体删除通知</b>") {
		t.Errorf("应以 🗑️ Emby 媒体删除通知 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "电影名称：") {
		t.Errorf("应含全角冒号 电影名称：, 实际 %s", out)
	}
	if !strings.Contains(out, "删除时间：") {
		t.Errorf("应含全角冒号 删除时间：, 实际 %s", out)
	}
}

// TestFormatDeletedSeriesNotification_UsesFormatMessage 验证剧集删除通知走 FormatMessage
func TestFormatDeletedSeriesNotification_UsesFormatMessage(t *testing.T) {
	item := &ItemDetail{Name: "被删剧集", SeriesName: "被删剧集"}
	episodes := []ItemDetail{{IndexNumber: 5, ParentIndexNumber: 2}}
	out := FormatDeletedSeriesNotification(item, episodes)
	if !strings.HasPrefix(out, "<b>🗑️ Emby 媒体删除通知</b>") {
		t.Errorf("应以 🗑️ Emby 媒体删除通知 开头, 实际 %s", out)
	}
	if !strings.Contains(out, "电视剧名称：") {
		t.Errorf("应含全角冒号 电视剧名称：, 实际 %s", out)
	}
}

// TestFormatPlaybackNotification_QMSStyle 验证播放通知为 qmediasync 风格（emoji 前缀 + 半角冒号）
func TestFormatPlaybackNotification_QMSStyle(t *testing.T) {
	item := &ItemDetail{
		Name:         "功夫熊猫",
		Type:         "Movie",
		RunTimeTicks: 600000000,
		Overview:     "熊猫打怪",
	}
	user := &UserInfo{Name: "测试用户"}
	out := FormatPlaybackNotification("playback.start", item, user, "iPhone", "Emby iOS", 0, true, true)
	if !strings.Contains(out, "播放开始") {
		t.Errorf("标题应含 播放开始, 实际 %s", out)
	}
	if !strings.Contains(out, "👤 用户: 测试用户") {
		t.Errorf("应含 👤 用户: 测试用户, 实际 %s", out)
	}
	if !strings.Contains(out, "📱 设备: iPhone (Emby iOS)") {
		t.Errorf("应含 📱 设备: iPhone (Emby iOS), 实际 %s", out)
	}
	// positionTicks=0 时显示时长而非进度
	if !strings.Contains(out, "⏱️ 时长") {
		t.Errorf("showProgress=true 且 positionTicks=0 应显示时长, 实际 %s", out)
	}
	if !strings.Contains(out, "📝 简介") {
		t.Errorf("showOverview=true 应显示简介段落, 实际 %s", out)
	}
}

// TestFormatPlaybackNotification_ShowProgressTrue 验证 showProgress=true 时显示进度
func TestFormatPlaybackNotification_ShowProgressTrue(t *testing.T) {
	item := &ItemDetail{
		Name: "电影X", Type: "Movie",
		RunTimeTicks: 600000000,
	}
	user := &UserInfo{Name: "u"}
	out := FormatPlaybackNotification("playback.pause", item, user, "dev", "client", 300000000, true, false)
	if !strings.Contains(out, "📊 播放进度") {
		t.Errorf("showProgress=true 应显示播放进度, 实际 %s", out)
	}
	if !strings.Contains(out, "50%") {
		t.Errorf("应含 50%%, 实际 %s", out)
	}
	if !strings.Contains(out, "⏱️ 观看时长") {
		t.Errorf("pause 事件应显示观看时长, 实际 %s", out)
	}
}

// TestFormatPlaybackNotification_ShowProgressFalse 验证 showProgress=false 时不显示进度
func TestFormatPlaybackNotification_ShowProgressFalse(t *testing.T) {
	item := &ItemDetail{
		Name: "电影Y", Type: "Movie",
		RunTimeTicks: 600000000,
		Overview:     "剧情",
	}
	user := &UserInfo{Name: "u"}
	out := FormatPlaybackNotification("playback.stop", item, user, "dev", "client", 300000000, false, false)
	if strings.Contains(out, "📊 播放进度") {
		t.Errorf("showProgress=false 不应显示播放进度, 实际 %s", out)
	}
	if strings.Contains(out, "⏱️ 时长") {
		t.Errorf("showProgress=false 不应显示时长字段, 实际 %s", out)
	}
	if !strings.Contains(out, "⏱️ 观看时长") {
		t.Errorf("stop 事件应显示观看时长, 实际 %s", out)
	}
}

// TestFormatPlaybackNotification_ShowOverviewTrueFalse 验证简介受开关控制
func TestFormatPlaybackNotification_ShowOverviewTrueFalse(t *testing.T) {
	item := &ItemDetail{
		Name: "电影Z", Type: "Movie",
		Overview: "这是一段剧情简介",
	}
	user := &UserInfo{Name: "u"}

	outOn := FormatPlaybackNotification("playback.start", item, user, "", "", 0, false, true)
	if !strings.Contains(outOn, "📝 简介") {
		t.Errorf("showOverview=true 应显示简介, 实际 %s", outOn)
	}

	outOff := FormatPlaybackNotification("playback.start", item, user, "", "", 0, false, false)
	if strings.Contains(outOff, "📝 简介") {
		t.Errorf("showOverview=false 不应显示简介, 实际 %s", outOff)
	}
}

// TestFormatPlaybackNotification_EpisodeSeasonEpisode 验证剧集播放通知含季集
func TestFormatPlaybackNotification_EpisodeSeasonEpisode(t *testing.T) {
	item := &ItemDetail{
		Name: "第5集", Type: "Episode",
		SeriesName: "电视剧A", ParentIndexNumber: 2, IndexNumber: 5,
	}
	user := &UserInfo{Name: "u"}
	out := FormatPlaybackNotification("playback.start", item, user, "TV", "Emby", 0, false, false)
	if !strings.Contains(out, "📺 电视剧: 电视剧A") {
		t.Errorf("应含 📺 电视剧: 电视剧A, 实际 %s", out)
	}
	if !strings.Contains(out, "🎬 季集: S2E5") {
		t.Errorf("应含 🎬 季集: S2E5, 实际 %s", out)
	}
}

// TestFormatPlaybackNotification_StopEmoji 验证播放停止通知用 ⏹️（对齐 QMS，非 ⛔）
func TestFormatPlaybackNotification_StopEmoji(t *testing.T) {
	item := &ItemDetail{Name: "电影", Type: "Movie"}
	user := &UserInfo{Name: "u"}
	out := FormatPlaybackNotification("playback.stop", item, user, "", "", 0, false, false)
	if !strings.Contains(out, "⏹️ 播放停止") {
		t.Errorf("stop 标题应含 ⏹️ 播放停止, 实际 %s", out)
	}
	if strings.Contains(out, "⛔") {
		t.Errorf("stop 不应再用 ⛔, 实际 %s", out)
	}
}
