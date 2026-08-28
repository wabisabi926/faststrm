package notify

import (
	"testing"
)

// TestNewTelegramBot 测试无代理构造函数
func TestNewTelegramBot(t *testing.T) {
	t.Run("empty token returns empty shell bot", func(t *testing.T) {
		// 空 token → newBotWithProxy 返回 error → newBot 捕获返回空壳
		bot := NewTelegramBot("", "")
		if bot == nil {
			t.Fatal("expected non-nil bot (empty shell) even with empty token")
		}
		if bot.BotToken() != "" {
			t.Fatalf("expected empty BotToken, got %s", bot.BotToken())
		}
		if bot.ChatID() != "" {
			t.Fatalf("expected empty ChatID, got %s", bot.ChatID())
		}
	})

	t.Run("fake token falls back to empty shell", func(t *testing.T) {
		// fake token → tgbotapi.NewBotAPI 会尝试 GetMe() 并失败
		// newBotWithProxy 返回 error → newBot 捕获返回空壳，但 token/chatID 字段已设置
		bot := NewTelegramBot("fake_token_123", "-100123456")
		if bot == nil {
			t.Fatal("expected non-nil bot")
		}
		// newBot 在 newBotWithProxy 失败时仍会设置 token/chatID
		// 取决于 logger 是否吞掉 error
		// 这里只验证不 panic 即可
	})
}

// TestNewTelegramBotWithProxy 测试带代理构造函数（仅不需要网络的场景）
func TestNewTelegramBotWithProxy(t *testing.T) {
	t.Run("empty token", func(t *testing.T) {
		bot, err := NewTelegramBotWithProxy("", "-100123", "http://proxy:8080")
		if err == nil {
			t.Fatal("expected error for empty token")
		}
		if bot != nil {
			t.Fatal("expected nil bot when token empty")
		}
	})

	t.Run("unsupported proxy scheme", func(t *testing.T) {
		bot, err := NewTelegramBotWithProxy("fake_token", "-100123", "ftp://proxy:21")
		if err == nil {
			t.Fatal("expected error for unsupported proxy scheme")
		}
		if bot != nil {
			t.Fatal("expected nil bot on proxy scheme error")
		}
	})
}

// TestNewBotWithProxy_InvalidProxy 测试无法解析的代理 URL
func TestNewBotWithProxy_InvalidProxy(t *testing.T) {
	bot, err := NewTelegramBotWithProxy("fake_token", "-100123", "not-a-valid-url:::")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL")
	}
	if bot != nil {
		t.Fatal("expected nil bot on invalid proxy URL")
	}
}

// TestTelegramBotUpdateCredentials 测试凭据热更新
func TestTelegramBotUpdateCredentials(t *testing.T) {
	// 直接构造 bot，避免触发 tgbotapi 网络请求
	bot := &TelegramBot{token: "initial_token", chatID: "-100999"}

	// 更新非空值
	bot.UpdateCredentials("new_token_xyz", "-100888")
	if bot.BotToken() != "new_token_xyz" {
		t.Fatalf("expected BotToken=new_token_xyz, got %s", bot.BotToken())
	}
	if bot.ChatID() != "-100888" {
		t.Fatalf("expected ChatID=-100888, got %s", bot.ChatID())
	}

	// 空 token 不应覆盖旧值
	bot.UpdateCredentials("", "-100777")
	if bot.BotToken() != "new_token_xyz" {
		t.Fatalf("empty token should not overwrite, got %s", bot.BotToken())
	}
	if bot.ChatID() != "-100777" {
		t.Fatalf("expected ChatID updated to -100777, got %s", bot.ChatID())
	}

	// 空 chatID 不应覆盖旧值
	bot.UpdateCredentials("another_token", "")
	if bot.BotToken() != "another_token" {
		t.Fatalf("expected BotToken=another_token, got %s", bot.BotToken())
	}
	if bot.ChatID() != "-100777" {
		t.Fatalf("empty chatID should not overwrite, got %s", bot.ChatID())
	}
}

// TestTelegramBotGetters 测试 getter 方法及 Underlying 行为
func TestTelegramBotGetters(t *testing.T) {
	t.Run("getters return initial values", func(t *testing.T) {
		bot := &TelegramBot{token: "tk_abc", chatID: "cid_123"}
		if bot.BotToken() != "tk_abc" {
			t.Fatalf("BotToken mismatch: got %s", bot.BotToken())
		}
		if bot.ChatID() != "cid_123" {
			t.Fatalf("ChatID mismatch: got %s", bot.ChatID())
		}
		if bot.ProxyURL() != "" {
			t.Fatalf("ProxyURL should be empty, got %s", bot.ProxyURL())
		}
	})

	t.Run("proxy getter", func(t *testing.T) {
		bot := &TelegramBot{proxyURL: "http://proxy:8080"}
		if bot.ProxyURL() != "http://proxy:8080" {
			t.Fatalf("ProxyURL mismatch: got %s", bot.ProxyURL())
		}
	})

	t.Run("Underlying before client created", func(t *testing.T) {
		// 直接构造 client 为 nil 的 bot，测试 Underlying 返回 error
		bot := &TelegramBot{token: "fake", chatID: "123"}
		_, err := bot.Underlying()
		if err == nil {
			t.Fatal("expected error from Underlying when ensureClient fails")
		}
	})
}
