package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
)

// ==================== 测试辅助 ====================

// fakeDispatcher 用于捕获通知调用
type fakeDispatcher struct {
	notifyCount  int32
	photoCount   int32
	lastMessage  string
	lastPhotoURL string
}

func (d *fakeDispatcher) Notify(ctx context.Context, msg string) error {
	atomic.AddInt32(&d.notifyCount, 1)
	d.lastMessage = msg
	return nil
}

func (d *fakeDispatcher) NotifyWithPhoto(ctx context.Context, caption, photoURL string) error {
	atomic.AddInt32(&d.photoCount, 1)
	d.lastMessage = caption
	d.lastPhotoURL = photoURL
	return nil
}

// makeSettings 构造 EmbySettings
func makeSettings(url, apiKey string) model.EmbySettings {
	return model.EmbySettings{
		URL:    url,
		APIKey: apiKey,
	}
}

// ==================== getClient 动态创建测试 ====================

// TestNotifierGetClient_NoConfig 未配置URL/APIKey时返回nil
func TestNotifierGetClient_NoConfig(t *testing.T) {
	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings("", "")
	})
	if c := n.getClient(); c != nil {
		t.Errorf("未配置时应返回 nil, 实际 %v", c)
	}
}

// TestNotifierGetClient_DynamicCreation 每次调用都创建新Client实例（对齐 qmediasync）
func TestNotifierGetClient_DynamicCreation(t *testing.T) {
	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings("http://test:8096", "key-1")
	})
	c1 := n.getClient()
	c2 := n.getClient()
	if c1 == nil || c2 == nil {
		t.Fatal("getClient 不应返回 nil")
	}
	if c1 == c2 {
		t.Errorf("每次 getClient 应创建新实例, 实际返回相同指针")
	}
}

// ==================== 用户ID缓存 + 配置变更失效测试 ====================

// TestNotifierUserID_ReuseAcrossClients 跨Client实例复用userID（核心修复场景）
// 模拟多次通知流程：第一次请求 /emby/Users，后续命中Notifier级缓存
func TestNotifierUserID_ReuseAcrossClients(t *testing.T) {
	var usersCallCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/Users" {
			atomic.AddInt32(&usersCallCount, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"Name":"admin","Id":"user-admin","Policy":{"EnableAllFolders":true}}]`))
			return
		}
		// 详情请求
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Id":"item-1","Name":"测试电影","Type":"Movie"}`))
	}))
	defer server.Close()

	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(server.URL, "key-1")
	})

	// 模拟3次独立通知流程，每次都通过 getClient() 创建新Client
	for i := 0; i < 3; i++ {
		c := n.getClient()
		if c == nil {
			t.Fatalf("第 %d 次 getClient 返回 nil", i+1)
		}
		// 触发用户ID获取
		_, err := c.GetItemDetail(context.Background(), "item-1")
		if err != nil {
			t.Fatalf("第 %d 次获取详情失败: %v", i+1, err)
		}
	}

	// 核心断言：/emby/Users 应只被请求1次（后续命中Notifier级缓存）
	if got := atomic.LoadInt32(&usersCallCount); got != 1 {
		t.Errorf("跨Client实例应复用userID, /emby/Users 应只调用1次, 实际 %d 次", got)
	}
}

// TestNotifierUserID_InvalidatesOnURLChange URL变更时清除userID缓存
func TestNotifierUserID_InvalidatesOnURLChange(t *testing.T) {
	var usersCallCount int32
	var currentURL string
	// 公共handler：/emby/Users 返回用户数组，其他路径返回单对象详情
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/emby/Users" {
			atomic.AddInt32(&usersCallCount, 1)
			w.Write([]byte(`[{"Name":"admin","Id":"user-admin","Policy":{"EnableAllFolders":true}}]`))
			return
		}
		// 详情端点返回单对象
		w.Write([]byte(`{"Id":"item-1","Name":"测试电影","Type":"Movie"}`))
	}
	server1 := httptest.NewServer(http.HandlerFunc(handler))
	defer server1.Close()
	server2 := httptest.NewServer(http.HandlerFunc(handler))
	defer server2.Close()

	currentURL = server1.URL
	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(currentURL, "key-1")
	})

	// 第一次：请求 server1
	c1 := n.getClient()
	c1.GetItemDetail(context.Background(), "item-1")
	if got := atomic.LoadInt32(&usersCallCount); got != 1 {
		t.Fatalf("第一次应请求1次 /emby/Users, 实际 %d", got)
	}

	// 切换到 server2
	currentURL = server2.URL
	c2 := n.getClient()
	c2.GetItemDetail(context.Background(), "item-1")
	// 配置变更后应重新请求 /emby/Users
	if got := atomic.LoadInt32(&usersCallCount); got != 2 {
		t.Errorf("URL变更后应重新请求 /emby/Users, 期望 2 次, 实际 %d 次", got)
	}

	// 再用 server2 配置创建Client, 应命中缓存
	c3 := n.getClient()
	c3.GetItemDetail(context.Background(), "item-1")
	if got := atomic.LoadInt32(&usersCallCount); got != 2 {
		t.Errorf("配置未变更应命中缓存, 期望仍为 2 次, 实际 %d 次", got)
	}
}

// TestNotifierUserID_InvalidatesOnAPIKeyChange APIKey变更时清除userID缓存
func TestNotifierUserID_InvalidatesOnAPIKeyChange(t *testing.T) {
	var usersCallCount int32
	var currentAPIKey string = "key-old"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/emby/Users" {
			atomic.AddInt32(&usersCallCount, 1)
			w.Write([]byte(`[{"Name":"admin","Id":"user-admin","Policy":{"EnableAllFolders":true}}]`))
			return
		}
		w.Write([]byte(`{"Id":"item-1","Name":"测试电影","Type":"Movie"}`))
	}))
	defer server.Close()

	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(server.URL, currentAPIKey)
	})

	// 第一次请求
	c1 := n.getClient()
	c1.GetItemDetail(context.Background(), "item-1")
	if got := atomic.LoadInt32(&usersCallCount); got != 1 {
		t.Fatalf("第一次应请求1次, 实际 %d", got)
	}

	// 修改APIKey
	currentAPIKey = "key-new"
	c2 := n.getClient()
	c2.GetItemDetail(context.Background(), "item-1")
	if got := atomic.LoadInt32(&usersCallCount); got != 2 {
		t.Errorf("APIKey变更后应重新请求, 期望 2 次, 实际 %d 次", got)
	}

	// APIKey不再变更, 应命中缓存
	c3 := n.getClient()
	c3.GetItemDetail(context.Background(), "item-1")
	if got := atomic.LoadInt32(&usersCallCount); got != 2 {
		t.Errorf("配置未变更应命中缓存, 期望仍为 2 次, 实际 %d 次", got)
	}
}

// ==================== 回调机制测试 ====================

// TestNotifierOnUserIDChange_Callback Client获取userID后回调通知Notifier
func TestNotifierOnUserIDChange_Callback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/emby/Users" {
			w.Write([]byte(`[{"Name":"admin","Id":"admin-id","Policy":{"EnableAllFolders":true}}]`))
			return
		}
		w.Write([]byte(`{"Id":"item-1","Name":"电影","Type":"Movie"}`))
	}))
	defer server.Close()

	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(server.URL, "key-1")
	})

	// 初始状态: 缓存为空
	n.userMu.Lock()
	if n.embyUserID != "" {
		t.Errorf("初始 embyUserID 应为空, 实际 %q", n.embyUserID)
	}
	n.userMu.Unlock()

	// 触发一次Client调用, 回调应写入Notifier缓存
	c := n.getClient()
	c.GetItemDetail(context.Background(), "item-1")

	n.userMu.Lock()
	if n.embyUserID != "admin-id" {
		t.Errorf("回调应将 embyUserID 缓存为 admin-id, 实际 %q", n.embyUserID)
	}
	if n.cachedURL != server.URL {
		t.Errorf("cachedURL 应为 %s, 实际 %s", server.URL, n.cachedURL)
	}
	if n.cachedAPIKey != "key-1" {
		t.Errorf("cachedAPIKey 应为 key-1, 实际 %s", n.cachedAPIKey)
	}
	n.userMu.Unlock()
}

// TestNotifierOnUserIDChange_InvalidateCallback Client失效时回调清除Notifier缓存
// 验证场景：第一次详情成功（缓存userID），第二次详情失败（触发InvalidateUserCache→回调清除Notifier缓存）
func TestNotifierOnUserIDChange_InvalidateCallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/emby/Users" {
			w.Write([]byte(`[{"Name":"admin","Id":"admin-id","Policy":{"EnableAllFolders":true}}]`))
			return
		}
		// item-1 返回成功，item-2 返回404触发失效
		if r.URL.Path == "/emby/Users/admin-id/Items/item-1" || r.URL.Path == "/emby/Items/item-1" {
			w.Write([]byte(`{"Id":"item-1","Name":"成功电影","Type":"Movie"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(server.URL, "key-1")
	})

	// 第一次调用 item-1: 获取userID并缓存到Notifier
	c := n.getClient()
	c.GetItemDetail(context.Background(), "item-1")

	n.userMu.Lock()
	if n.embyUserID != "admin-id" {
		t.Fatalf("首次调用后 embyUserID 应为 admin-id, 实际 %q", n.embyUserID)
	}
	n.userMu.Unlock()

	// 第二次调用 item-2: 详情404 → InvalidateUserCache → 回调清除Notifier缓存
	c2 := n.getClient()
	c2.GetItemDetail(context.Background(), "item-2")

	n.userMu.Lock()
	if n.embyUserID != "" {
		t.Errorf("InvalidateUserCache 回调应清除 embyUserID, 实际 %q", n.embyUserID)
	}
	n.userMu.Unlock()
}

// ==================== InvalidateClientCache 测试 ====================

// TestNotifierInvalidateClientCache 手动清除所有缓存（配置变更时由handler调用）
func TestNotifierInvalidateClientCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"Name":"admin","Id":"admin-id","Policy":{"EnableAllFolders":true}}]`))
	}))
	defer server.Close()

	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(server.URL, "key-1")
	})

	// 触发一次填充缓存
	c := n.getClient()
	c.getEmbyUserID(context.Background())

	// 验证缓存已填充
	n.userMu.Lock()
	if n.embyUserID == "" || n.cachedURL == "" || n.cachedAPIKey == "" {
		t.Fatalf("缓存应已填充, 实际 userID=%q url=%q key=%q",
			n.embyUserID, n.cachedURL, n.cachedAPIKey)
	}
	n.userMu.Unlock()

	// 调用 InvalidateClientCache 清除
	n.InvalidateClientCache()

	n.userMu.Lock()
	if n.embyUserID != "" || n.cachedURL != "" || n.cachedAPIKey != "" {
		t.Errorf("InvalidateClientCache 应清除所有缓存, 实际 userID=%q url=%q key=%q",
			n.embyUserID, n.cachedURL, n.cachedAPIKey)
	}
	n.userMu.Unlock()
}

// ==================== 并发安全测试 ====================

// TestNotifierUserID_ConcurrentAccess 并发场景下缓存正确
func TestNotifierUserID_ConcurrentAccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/emby/Users" {
			w.Write([]byte(`[{"Name":"admin","Id":"admin-id","Policy":{"EnableAllFolders":true}}]`))
			return
		}
		w.Write([]byte(`{"Id":"item-1","Name":"电影","Type":"Movie"}`))
	}))
	defer server.Close()

	n := NewNotifier(&fakeDispatcher{}, func() model.EmbySettings {
		return makeSettings(server.URL, "key-1")
	})

	done := make(chan struct{})
	const goroutines = 10
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			c := n.getClient()
			if c == nil {
				t.Error("getClient 返回 nil")
				return
			}
			// 并发触发 getEmbyUserID
			c.getEmbyUserID(context.Background())
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < goroutines; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("goroutine 超时")
		}
	}

	// 最终缓存应为 admin-id
	n.userMu.Lock()
	if n.embyUserID != "admin-id" {
		t.Errorf("并发后 embyUserID 应为 admin-id, 实际 %q", n.embyUserID)
	}
	n.userMu.Unlock()
}
