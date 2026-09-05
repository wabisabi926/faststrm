package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ==================== 辅助函数 ====================

func setupTestServer(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	client := NewClient(server.URL, "test-api-key")
	return client, server
}

// ==================== GetAllUsers 测试 ====================

func TestGetAllUsers(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Users" {
			t.Errorf("请求路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"Name":"user1","Id":"user-1","Policy":{"EnableAllFolders":true}},
			{"Name":"user2","Id":"user-2","Policy":{"EnableAllFolders":false}}
		]`))
	})
	defer server.Close()

	users, err := client.GetAllUsers(context.Background())
	if err != nil {
		t.Fatalf("GetAllUsers 失败: %v", err)
	}

	if len(users) != 2 {
		t.Errorf("期望 2 个用户，实际 %d 个", len(users))
	}
}

func TestGetAllUsers_EmptyList(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
	defer server.Close()

	users, err := client.GetAllUsers(context.Background())
	if err != nil {
		t.Fatalf("GetAllUsers 失败: %v", err)
	}

	if len(users) != 0 {
		t.Errorf("期望 0 个用户，实际 %d 个", len(users))
	}
}

func TestGetAllUsers_ServerError(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer server.Close()

	_, err := client.GetAllUsers(context.Background())
	if err == nil {
		t.Error("期望错误，实际 nil")
	}
}

// ==================== GetUsersWithAllLibrariesAccess 测试 ====================

func TestGetUsersWithAllLibrariesAccess(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Users" {
			t.Errorf("请求路径错误: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"Name":"admin","Id":"user-admin","Policy":{"EnableAllFolders":true}},
			{"Name":"regular","Id":"user-regular","Policy":{"EnableAllFolders":false}}
		]`))
	})
	defer server.Close()

	users, err := client.GetUsersWithAllLibrariesAccess(context.Background())
	if err != nil {
		t.Fatalf("GetUsersWithAllLibrariesAccess 失败: %v", err)
	}

	if len(users) != 1 {
		t.Errorf("期望 1 个有权限用户，实际 %d 个", len(users))
	}

	if users[0].ID != "user-admin" {
		t.Errorf("期望用户 ID 为 user-admin，实际 %s", users[0].ID)
	}
}

func TestGetUsersWithAllLibrariesAccess_NoAdmin(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"Name":"user1","Id":"user-1","Policy":{"EnableAllFolders":false}},
			{"Name":"user2","Id":"user-2","Policy":{"EnableAllFolders":false}}
		]`))
	})
	defer server.Close()

	users, err := client.GetUsersWithAllLibrariesAccess(context.Background())
	if err != nil {
		t.Fatalf("GetUsersWithAllLibrariesAccess 失败: %v", err)
	}

	if len(users) != 0 {
		t.Errorf("期望 0 个有权限用户，实际 %d 个", len(users))
	}
}

// ==================== tryGetDetailWithAnyUser 测试 ====================

func TestTryGetDetailWithAnyUser_Success(t *testing.T) {
	callCount := 0
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		// 获取用户列表
		if r.URL.Path == "/emby/Users" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"Name":"user1","Id":"user-1","Policy":{"EnableAllFolders":false}},
				{"Name":"user2","Id":"user-2","Policy":{"EnableAllFolders":false}}
			]`))
			return
		}

		// 尝试第一个用户获取详情
		if r.URL.Path == "/emby/Users/user-1/Items/test-id" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"Id":"test-id",
				"Name":"测试电影",
				"Type":"Movie",
				"CommunityRating":8.5
			}`))
			return
		}

		// 默认返回 404
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()

	detail, err := client.tryGetDetailWithAnyUser(context.Background(), "test-id")
	if err != nil {
		t.Fatalf("tryGetDetailWithAnyUser 失败: %v", err)
	}

	if detail.ID != "test-id" {
		t.Errorf("期望 ID 为 test-id，实际 %s", detail.ID)
	}

	if detail.Name != "测试电影" {
		t.Errorf("期望 Name 为 测试电影，实际 %s", detail.Name)
	}

	t.Logf("调用次数: %d", callCount)
}

func TestTryGetDetailWithAnyUser_AllUsersFail(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/emby/Users" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"Name":"user1","Id":"user-1","Policy":{"EnableAllFolders":false}},
				{"Name":"user2","Id":"user-2","Policy":{"EnableAllFolders":false}}
			]`))
			return
		}

		// 所有用户都返回 404
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()

	_, err := client.tryGetDetailWithAnyUser(context.Background(), "test-id")
	if err == nil {
		t.Error("期望错误，实际 nil")
	}
}

func TestTryGetDetailWithAnyUser_NoUsers(t *testing.T) {
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
	defer server.Close()

	_, err := client.tryGetDetailWithAnyUser(context.Background(), "test-id")
	if err == nil {
		t.Error("期望错误（无用户），实际 nil")
	}
}

// ==================== GetItemDetailWithRetry 测试 ====================

func TestGetItemDetailWithRetry_404Retry(t *testing.T) {
	callCount := 0
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		// 获取用户列表
		if r.URL.Path == "/emby/Users" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"Name":"admin","Id":"user-1","Policy":{"EnableAllFolders":true}}]`))
			return
		}

		// 前两次返回 404，第三次成功
		if callCount <= 4 { // 2 次用户请求 + 2 次详情请求
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"Id":"test-id",
			"Name":"测试电影",
			"Type":"Movie",
			"CommunityRating":8.5
		}`))
	})
	defer server.Close()

	// 缩短重试间隔以加速测试
	client.http = &http.Client{Timeout: 2 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	detail, err := client.GetItemDetailWithRetry(ctx, "test-id")
	if err != nil {
		t.Fatalf("GetItemDetailWithRetry 失败: %v", err)
	}

	if detail.Name != "测试电影" {
		t.Errorf("期望 Name 为 测试电影，实际 %s", detail.Name)
	}

	t.Logf("调用次数: %d", callCount)
}

func TestGetItemDetailWithRetry_MaxRetries(t *testing.T) {
	// 覆盖退避序列以加速测试（默认序列总等待约 15.5s）
	oldDelays := getDetailRetryDelays
	getDetailRetryDelays = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond}
	defer func() { getDetailRetryDelays = oldDelays }()

	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/emby/Users" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"Name":"admin","Id":"user-1","Policy":{"EnableAllFolders":true}}]`))
			return
		}

		// 一直返回 404
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := client.GetItemDetailWithRetry(ctx, "test-id")
	if err == nil {
		t.Error("期望错误（重试耗尽），实际 nil")
	}
}

func TestGetItemDetailWithRetry_MetadataEmptyRetries(t *testing.T) {
	// 覆盖退避序列以加速测试
	oldDelays := getDetailRetryDelays
	getDetailRetryDelays = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond}
	defer func() { getDetailRetryDelays = oldDelays }()

	callCount := 0
	client, server := setupTestServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/emby/Users" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"Name":"admin","Id":"user-1","Policy":{"EnableAllFolders":true}}]`))
			return
		}

		// 前两次详情请求返回 200 但元数据为空（模拟 Emby 刮削未完成），之后返回完整数据
		if callCount <= 2 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"Id":"test-id","Name":"测试剧集","Type":"Series"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"Id":"test-id",
			"Name":"测试剧集",
			"Type":"Series",
			"Overview":"一段简介",
			"Genres":["剧情"],
			"People":[{"Name":"演员A","Type":"Actor"}],
			"CommunityRating":8.5,
			"ImageTags":{"Primary":"tag1"}
		}`))
	})
	defer server.Close()

	client.http = &http.Client{Timeout: 2 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	detail, err := client.GetItemDetailWithRetry(ctx, "test-id")
	if err != nil {
		t.Fatalf("GetItemDetailWithRetry 失败: %v", err)
	}
	if detail.Overview == "" {
		t.Errorf("期望重试后拿到完整元数据，实际 Overview 为空")
	}
	if callCount < 3 {
		t.Errorf("期望至少 3 次请求（空元数据触发重试），实际 %d 次", callCount)
	}
	t.Logf("调用次数: %d", callCount)
}

// ==================== InvalidateUserCache 测试 ====================

func TestInvalidateUserCache(t *testing.T) {
	client := NewClient("http://test.com", "test-api-key")

	// 手动设置缓存
	client.mu.Lock()
	client.embyUserID = "cached-user-id"
	client.mu.Unlock()

	// 验证缓存已设置
	client.mu.Lock()
	if client.embyUserID != "cached-user-id" {
		t.Error("缓存未正确设置")
	}
	client.mu.Unlock()

	// 清除缓存
	client.InvalidateUserCache()

	// 验证缓存已清除
	client.mu.Lock()
	if client.embyUserID != "" {
		t.Error("缓存未正确清除")
	}
	client.mu.Unlock()
}

// ==================== 边界条件测试 ====================

func TestGetItemDetail_InvalidParams(t *testing.T) {
	client := NewClient("", "")

	_, err := client.GetItemDetail(context.Background(), "")
	if err == nil {
		t.Error("期望错误（无效参数），实际 nil")
	}
}

func TestGetAllUsers_InvalidConfig(t *testing.T) {
	client := NewClient("", "")

	_, err := client.GetAllUsers(context.Background())
	if err == nil {
		t.Error("期望错误（未配置），实际 nil")
	}
}

func TestGetUsersWithAllLibrariesAccess_InvalidConfig(t *testing.T) {
	client := NewClient("", "")

	_, err := client.GetUsersWithAllLibrariesAccess(context.Background())
	if err == nil {
		t.Error("期望错误（未配置），实际 nil")
	}
}
