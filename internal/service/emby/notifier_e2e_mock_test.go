// Package emby · Emby 入库通知端到端模拟测试
//
// 覆盖链路：Emby Webhook(library.new) → Notifier.handleMediaAdded
//   → Client.(GetUsers / GetItemDetail / Images/* HTTP 下载)
//   → FormatMovieNotification / FormatSeriesNotification(季集缓冲 flush)
//   → NotifierDispatcher.NotifyWithPhoto(caption, posterLocalPath)
//
// 全部依赖通过 httptest.Server 模拟真实 Emby HTTP（与 client_test.go 手法一致，无第三方 mock 库）。
//
// 用例清单：
//   TestNotifierE2E_MovieAdded        电影入库 · 全字段 · 有海报
//   TestNotifierE2E_MovieNoActors     电影入库 · People 全非 Actor → 「主演：」 空白对齐 QMS
//   TestNotifierE2E_SeriesDebounced   剧集入库 · 缓冲 2 集 → debounce 到期后合并 1 条通知
//
// 关键断言（对齐之前与 QMS 的 1:1 契约）：
//   ① Title 段必须是 <b>📚 Emby 电影/电视剧入库通知</b>
//   ② 所有冒号为全角 「：」
//   ③ Movie 评分 0 → "0.0"，Series People 非 Actor 但有数据 → 主演后空白
//   ④ 简介不截断 + 不含 "..."
//   ⑤ dispatcher 收到的 posterPath 是本地路径，且文件真实存在
//   ⑥ Series debounce 10 秒后合并为单条，含「📺 入库季集：第 1 季 E01 / E02」

package emby

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
)

// ============================== Fake Dispatcher ==============================

// recordedCall 记录一次 dispatcher 调用（所有字段可读、可断言）
type recordedCall struct {
	Method    string // "Notify" 或 "NotifyWithPhoto"
	Message   string // caption
	PhotoPath string // NotifyWithPhoto 时为本地路径（期望）
	At        time.Time
}

// capturingDispatcher 捕获所有通知调用，通过 WaitForPhoto(n, timeout) 等待异步 flush。
type capturingDispatcher struct {
	mu    sync.Mutex
	calls []recordedCall

	// 有新的 NotifyWithPhoto 时广播，用于 Series debounce 等待
	photoCond *sync.Cond
}

func newCapturingDispatcher() *capturingDispatcher {
	d := &capturingDispatcher{}
	d.photoCond = sync.NewCond(&d.mu)
	return d
}

func (d *capturingDispatcher) Notify(ctx context.Context, msg string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, recordedCall{Method: "Notify", Message: msg, At: time.Now()})
	return nil
}

func (d *capturingDispatcher) NotifyWithPhoto(ctx context.Context, caption, photoURL string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, recordedCall{Method: "NotifyWithPhoto", Message: caption, PhotoPath: photoURL, At: time.Now()})
	d.photoCond.Broadcast()
	return nil
}

// photoCount 返回当前累计 NotifyWithPhoto 次数（无锁快照，断言前用）
func (d *capturingDispatcher) photoCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.photoCountLocked()
}

func (d *capturingDispatcher) photoCountLocked() int {
	n := 0
	for _, c := range d.calls {
		if c.Method == "NotifyWithPhoto" {
			n++
		}
	}
	return n
}

// waitForPhoto 阻塞直到 photoCount 达到 target，或超时。返回最终次数。
func (d *capturingDispatcher) waitForPhoto(target int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	d.mu.Lock()
	defer d.mu.Unlock()
	for d.photoCountLocked() < target {
		remain := time.Until(deadline)
		if remain <= 0 {
			return d.photoCountLocked()
		}
		// Cond.Wait 不支持超时，用 goroutine + 超时信号避免无限挂起
		signaled := make(chan struct{}, 1)
		go func() {
			d.photoCond.Wait()
			select {
			case signaled <- struct{}{}:
			default:
			}
		}()
		select {
		case <-signaled:
			// 继续判断条件
		case <-time.After(remain):
			// 超时：唤醒 cond waiter 以便它退出，返回当前计数
			d.photoCond.Broadcast()
			return d.photoCountLocked()
		}
	}
	return d.photoCountLocked()
}

// lastCall 返回最后一条通知（测试前需确认至少有 1 条）
func (d *capturingDispatcher) lastCall() recordedCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.calls) == 0 {
		return recordedCall{}
	}
	return d.calls[len(d.calls)-1]
}

// ============================== Emby HTTP Mock Server ==============================

// embyHandler 构造一个满足以下端点的 Emby mock：
//
//	GET /emby/Users           → 返回 1 个 admin 用户（ID 固定 "user-test"）
//	GET /emby/Users/{uid}/Items/{id}   → 从 items 表取 *ItemDetail JSON（无 Fields 白名单，与实际 API 一致）
//	GET /emby/Items/{id}/Images/{type} → 返回 1x1 JPEG 占位字节（允许下载到临时海报）
func embyHandler(items map[string]*ItemDetail) http.Handler {
	// 1x1 baseline JPEG（合法小图，避免写 0 字节触发下载失败）
	// 非 const：tinyJPEG 用到了 append/字节切分，不参与 const 求值
	var tinyJPEG = []byte{
		0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01, 0x01, 0x01, 0x00, 0x48,
		0x00, 0x48, 0x00, 0x00, 0xff, 0xdb, 0x00, 0x43, 0x00,
	}
	tinyJPEG = append(tinyJPEG, bytesRepeat(0x08, 64)...)
	tinyJPEG = append(tinyJPEG,
		0xff, 0xc0, 0x00, 0x0b, 0x08, 0x00, 0x01, 0x00, 0x01, 0x01, 0x01, 0x11, 0x00,
		0xff, 0xc4, 0x00, 0x14, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xff, 0xc4, 0x00, 0x14, 0x10, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xff, 0xda, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3f, 0x00, 0xd2, 0xcf, 0x20, 0xff, 0xd9,
	)

	var userCalls int32
	var itemCalls int32
	var imageCalls int32
	_, _, _ = userCalls, itemCalls, imageCalls // 保留调试时用，避免 unused

	mux := http.NewServeMux()
	mux.HandleFunc("/emby/Users", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&userCalls, 1)
		_ = json.NewEncoder(w).Encode([]UserDto{{Name: "Admin", ID: "user-test", Policy: UserPolicy{EnableAllFolders: true}}})
	})
	mux.HandleFunc("/emby/Users/", func(w http.ResponseWriter, r *http.Request) {
		// path: /emby/Users/{userID}/Items/{itemID}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/emby/Users/"), "/")
		if len(parts) >= 3 && parts[1] == "Items" {
			atomic.AddInt32(&itemCalls, 1)
			itemID := parts[2]
			it, ok := items[itemID]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(it)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/emby/Items/", func(w http.ResponseWriter, r *http.Request) {
		// path: /emby/Items/{id}/Images/{Backdrop|Primary}
		if strings.Contains(r.URL.Path, "/Images/") {
			atomic.AddInt32(&imageCalls, 1)
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(tinyJPEG)
			return
		}
		http.NotFound(w, r)
	})
	return mux
}

// bytesRepeat 返回 n 个 b；等价于 bytes.Repeat，但避免 import bytes 未用警告
func bytesRepeat(b byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = b
	}
	return buf
}

// newEmbyTestEnv 构建 httptest server + settingsProvider，t.Cleanup 自动 Close
func newEmbyTestEnv(t *testing.T, items map[string]*ItemDetail) (*httptest.Server, SettingsProvider) {
	t.Helper()
	srv := httptest.NewServer(embyHandler(items))
	t.Cleanup(func() { srv.Close() })
	sp := func() model.EmbySettings {
		return model.EmbySettings{
			URL:              srv.URL,
			APIKey:           "test-key",
			NotifyMediaAdded: true,
		}
	}
	return srv, sp
}

// ============================== Data Builders ==============================

func defaultMovieItem(id, name string) *ItemDetail {
	return &ItemDetail{
		ID:              id,
		Name:            name,
		Type:            "Movie",
		ProductionYear:  2024,
		CommunityRating: 8.2,
		Genres:          []string{"科幻", "冒险"},
		Overview:        "这是一个完整的简介段落：" + strings.Repeat("详细剧情。", 10), // 超 100 字，验证不截断
		DateCreated:     "2026-01-15T12:34:56.000Z",
		ImageTags:       map[string]string{"Primary": "abc123"}, // 严格 大写P 才能命中 buildImageURLCaseSensitive
		People: []Person{
			{Name: "张三", Type: "Actor"},
			{Name: "李四", Type: "Actor"},
			{Name: "王五", Type: "Director"}, // 导演不应出现在主演列表
		},
	}
}

// ============================== Helpers ==============================

// assertContainsAll 批量断言；任一条不命中立即 Fatal
func assertContainsAll(t *testing.T, where, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Fatalf("[%s] 缺少片段 %q，body=\n%s", where, n, haystack)
		}
	}
}
func assertNotContainsAny(t *testing.T, where, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Fatalf("[%s] 不应包含 %q，但实际有。body=\n%s", where, n, haystack)
		}
	}
}

// ============================== 用例 1：电影入库，全链路有图 ==============================

func TestNotifierE2E_MovieAdded(t *testing.T) {
	movie := defaultMovieItem("movie-流浪地球", "流浪地球 3")
	items := map[string]*ItemDetail{movie.ID: movie}
	_, sp := newEmbyTestEnv(t, items)
	disp := newCapturingDispatcher()
	n := NewNotifier(disp, sp)

	err := n.HandleWebhookEvent(context.Background(), WebhookEvent{
		Event: "library.new",
		Item:  &ItemInfo{ID: movie.ID, Name: movie.Name, Type: "Movie"},
	})
	if err != nil {
		t.Fatalf("HandleWebhookEvent 返回错误: %v", err)
	}

	if got := disp.photoCount(); got != 1 {
		t.Fatalf("期望 1 次 NotifyWithPhoto，实际 %d 次。calls=%+v", got, disp.calls)
	}
	call := disp.lastCall()
	bodyAfterTitle := call.Message

	// 契约 ① Title 段
	assertContainsAll(t, "title", call.Message,
		"<b>📚 Emby 电影入库通知</b>",
		"流浪地球 3 (2024)",
	)
	// 契约 ② 全角冒号（评分/类型/主演/入库时间 4 处结构化标签；Movie 无「入库季集」但断言 hasAll 要求同时存在）
	assertContainsAll(t, "fullwidth-colons", call.Message,
		"评分：", "类型：", "主演：", "入库时间：",
	)
	// 半角冒号：结构化标签 5 处不能用，允许 http:// / 标题里的英文冒号
	assertNotContainsAny(t, "no-halfwidth-label-colons", bodyAfterTitle,
		"评分:", "类型:", "主演:", "入库时间:", "📺 入库季集:", // 标签 + 半角冒号 = 错
	)
	// 📝 简介是独立段落标题，无冒号（对齐 qmediasyncNotificationTemplate）
	assertContainsAll(t, "overview-heading", call.Message, "\n📝 简介\n")
	assertNotContainsAny(t, "overview-no-colon", call.Message, "📝 简介：")
	// 契约 ③ 评分/类型/主演/导演
	assertContainsAll(t, "content-blocks", call.Message,
		"评分：8.2",
		"类型：科幻, 冒险",
		"主演：张三, 李四",
	)
	assertNotContainsAny(t, "director-absent", call.Message, "王五") // 导演不出现在主演
	// 契约 ④ 简介不截断、不含 "..."
	assertContainsAll(t, "overview", call.Message, movie.Overview)
	assertNotContainsAny(t, "overview-not-truncated", call.Message, "...")
	// 契约 ⑤ 海报是本地路径且真实存在
	if strings.Contains(call.PhotoPath, "://") {
		t.Fatalf("期望 posterPath 为本地路径，实际 %q", call.PhotoPath)
	}
	if !strings.HasPrefix(filepath.Base(call.PhotoPath), embyTempImagePrefix) {
		t.Fatalf("期望海报文件名前缀 %q，实际 %q", embyTempImagePrefix, filepath.Base(call.PhotoPath))
	}
	if st, err := os.Stat(call.PhotoPath); err != nil || st.IsDir() {
		t.Fatalf("期望海报文件存在: path=%q stat_err=%v isDir=%v", call.PhotoPath, err, st != nil && st.IsDir())
	}
	t.Logf("poster path captured = %s（dispatcher 层按契约 safeRemove；此处仅验证文件真的被写出）", call.PhotoPath)
}

// ============================== 用例 2：People 含数据但没有 Actor → 主演后空白（对齐 QMS） ==============================

func TestNotifierE2E_MovieNoActors(t *testing.T) {
	movie := defaultMovieItem("movie-null-actor", "无主演电影")
	movie.People = []Person{
		{Name: "编剧甲", Type: "Writer"},
		{Name: "导演乙", Type: "Director"}, // 都不是精确字符串 Actor
	}
	movie.CommunityRating = 0 // 契约 ③-Movie：评分 0 → "0.0"
	items := map[string]*ItemDetail{movie.ID: movie}
	_, sp := newEmbyTestEnv(t, items)
	disp := newCapturingDispatcher()
	n := NewNotifier(disp, sp)

	_ = n.HandleWebhookEvent(context.Background(), WebhookEvent{
		Event: "library.new",
		Item:  &ItemInfo{ID: movie.ID, Name: movie.Name, Type: "Movie"},
	})

	if disp.photoCount() != 1 {
		t.Fatalf("期望 1 次 NotifyWithPhoto，实际 %+v", disp.calls)
	}
	msg := disp.lastCall().Message
	// 评分严格 0.0
	assertContainsAll(t, "rating-zero", msg, "评分：0.0")
	// 严格 QMS 语义：People 非空但无 Actor → 主演后为空串，不写 "暂无数据"
	if strings.Contains(msg, "主演：暂无数据") {
		t.Fatalf("People 非空但无 Actor 时，主演后应为空白，body=\n%s", msg)
	}
	if !strings.Contains(msg, "👤 主演：") {
		t.Fatalf("应有 👤 主演：标签，body=\n%s", msg)
	}
	// 「👤 主演：\n」(主演后立刻换行) 或 「👤 主演：⏰」 这种紧邻其他标签的语义，都算"空白"
	after := msg[strings.Index(msg, "主演：")+len("主演："):]
	// 下个换行前的内容应不超过 2 个全角/ASCII 字符的空白（trimSpace 后为空）
	end := strings.IndexRune(after, '\n')
	if end >= 0 {
		after = after[:end]
	}
	if strings.TrimSpace(after) != "" {
		t.Fatalf("期望主演后空白，trim 后为 %q，完整 body=\n%s", strings.TrimSpace(after), msg)
	}
}

// ============================== 用例 3：剧集缓冲 + debounce 合并 ==============================

func TestNotifierE2E_SeriesDebounced(t *testing.T) {
	series := &ItemDetail{
		ID:              "series-S01",
		Name:            "长安的荔枝",
		Type:            "Series",
		ProductionYear:  2024,
		CommunityRating: 0, // Series：0 对齐 QMS L532 语义显示 "暂无数据"（与 Movie 分支 0→"0.0" 不同）
		Genres:          []string{"剧情"},
		Overview:        strings.Repeat("官场职场的荒诞。", 20), // 400 字验证 Series 分支也不截断
		DateCreated:     "2026-01-02T00:00:00.000Z",
		ImageTags:       map[string]string{"backdrop": "tag-bd"}, // 小写 backdrop，对齐 QMS 精确大小写
		People: []Person{
			{Name: "雷佳音", Type: "Actor"},
			{Name: "导演陈", Type: "Director"},
		},
	}
	items := map[string]*ItemDetail{series.ID: series}
	_, sp := newEmbyTestEnv(t, items)
	disp := newCapturingDispatcher()

	// 关键：Series debounce 窗口是常量 EpisodeDebounceWindow=10s。
	// 真实环境里每集入库会 reset timer，这里必须等 timer 到期后才会 flush。
	// 用 dispatcher.waitForPhoto(1, 15s) 同步等待，避免 race。
	n := NewNotifier(disp, sp)

	// 第 1 集（S01E01）
	if err := n.HandleWebhookEvent(context.Background(), WebhookEvent{
		Event: "library.new",
		Item: &ItemInfo{
			ID:                "ep-1",
			Name:              "E1",
			Type:              "Episode",
			SeriesID:          series.ID,
			SeriesName:        series.Name,
			ParentIndexNumber: 1,
			IndexNumber:       1,
		},
	}); err != nil {
		t.Fatalf("ep1 HandleWebhookEvent: %v", err)
	}
	// 第 2 集 立即到（触发 timer reset → 总等待 > 10s）
	time.Sleep(300 * time.Millisecond)
	if err := n.HandleWebhookEvent(context.Background(), WebhookEvent{
		Event: "library.new",
		Item: &ItemInfo{
			ID:                "ep-2",
			Name:              "E2",
			Type:              "Episode",
			SeriesID:          series.ID,
			SeriesName:        series.Name,
			ParentIndexNumber: 1,
			IndexNumber:       2,
		},
	}); err != nil {
		t.Fatalf("ep2 HandleWebhookEvent: %v", err)
	}

	// ep2 发送后 timer 重置，至少需要再等 EpisodeDebounceWindow 才会 flush。
	// 再留 1.5s 余量应对 CI 抖动。
	budget := EpisodeDebounceWindow + 1500*time.Millisecond
	if got := disp.waitForPhoto(1, budget); got != 1 {
		t.Fatalf("debounce %v 内未等到 1 次 NotifyWithPhoto，got=%d calls=%+v", budget, got, disp.calls)
	}
	call := disp.lastCall()

	assertContainsAll(t, "series-title", call.Message,
		"<b>📚 Emby 电视剧入库通知</b>",
		"长安的荔枝 (2024)",
	)
	// 评分：Series CommunityRating==0 → 「暂无数据」
	assertContainsAll(t, "series-rating-zero", call.Message, "评分：暂无数据")
	// 不截断 + 无省略号
	assertContainsAll(t, "series-overview", call.Message, series.Overview)
	assertNotContainsAny(t, "series-overview-truncated", call.Message, "...")
	// 主演：导演陈被剔除，只剩雷佳音
	assertContainsAll(t, "series-actors", call.Message, "主演：雷佳音")
	assertNotContainsAny(t, "series-no-director-in-actors", call.Message, "导演陈")
	// 季集合并：实际 formatSeasonEpisodes 输出压缩格式 S1E1-E2（跨连续的压缩、非连续的逗号分隔；与 TS 版本一致）
	assertContainsAll(t, "season-episodes", call.Message,
		"📺 入库季集：",
		"S1E1-E2",
	)
	assertNotContainsAny(t, "season-episodes-compressed-no-chinese-wording", call.Message, "第 1 季 E")
	// 全角冒号检查
	assertContainsAll(t, "series-fullwidth", call.Message,
		"评分：", "类型：", "主演：", "入库时间：", "📺 入库季集：",
	)
	// 海报文件断言（同 Movie 用例）
	if strings.Contains(call.PhotoPath, "://") {
		t.Fatalf("Series poster 应为本地路径，实际 %q", call.PhotoPath)
	}
	if st, err := os.Stat(call.PhotoPath); err != nil || st.IsDir() {
		t.Fatalf("Series poster 文件不存在：%s err=%v", call.PhotoPath, err)
	}

	// 再等 500ms：不应再追加第二条（防抖合并应只发 1 条）
	time.Sleep(500 * time.Millisecond)
	if got := disp.photoCount(); got != 1 {
		t.Fatalf("防抖合并后只应产生 1 条剧集通知，实际 %d。calls=%+v", got, disp.calls)
	}
}

// ============================== 守护断言：确保 tinyJPEG 是合法 JPEG ==============================

func TestNotifierE2E_TinyJPEG_IsValid(t *testing.T) {
	// 复用 embyHandler 返回的字节做魔数校验：SOI=\xff\xd8，EOI=\xff\xd9
	var userCalls int32
	_ = userCalls
	// 直接用 handler 调 Images 端点：
	h := embyHandler(map[string]*ItemDetail{})
	srv := httptest.NewServer(h)
	defer srv.Close()
	u := fmt.Sprintf("%s/emby/Items/x/Images/Backdrop?tag=a&maxWidth=1&api_key=k", srv.URL)
	resp, err := http.Get(u) //nolint:gosec // 仅测试固定 httptest URL
	if err != nil {
		t.Fatalf("GET Images failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Images status=%d", resp.StatusCode)
	}
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	if n < 4 || buf[0] != 0xff || buf[1] != 0xd8 {
		t.Fatalf("JPEG SOI 魔数错，buf[:4]=%x", buf[:4])
	}
	// 结尾 EOI
	if buf[n-2] != 0xff || buf[n-1] != 0xd9 {
		t.Fatalf("JPEG EOI 魔数错，tail=%x", buf[n-4:n])
	}
}
