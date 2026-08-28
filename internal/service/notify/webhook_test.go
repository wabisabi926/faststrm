package notify

import (
	"context"
	"testing"
)

// ==================== NewWebhookSender ====================

func TestNewWebhookSender(t *testing.T) {
	t.Run("empty url returns nil", func(t *testing.T) {
		got := NewWebhookSender("")
		if got != nil {
			t.Fatalf("NewWebhookSender(\"\") = %v, want nil", got)
		}
	})

	t.Run("non-empty url returns non-nil", func(t *testing.T) {
		got := NewWebhookSender("http://example.com/hook")
		if got == nil {
			t.Fatal("NewWebhookSender with url should not be nil")
		}
	})

	t.Run("any non-empty url works", func(t *testing.T) {
		urls := []string{
			"http://localhost:8080",
			"https://example.com/webhook",
			"https://hooks.example.com/path?token=xyz",
			"webhook://custom/scheme",
		}
		for _, u := range urls {
			got := NewWebhookSender(u)
			if got == nil {
				t.Fatalf("NewWebhookSender(%q) should not be nil", u)
			}
		}
	})
}

// ==================== IsConfigured ====================

func TestWebhookSenderIsConfigured(t *testing.T) {
	t.Run("nil pointer returns false", func(t *testing.T) {
		var w *WebhookSender
		if w.IsConfigured() {
			t.Fatal("nil WebhookSender.IsConfigured() should be false")
		}
	})

	t.Run("zero value (empty url) returns false", func(t *testing.T) {
		w := &WebhookSender{}
		if w.IsConfigured() {
			t.Fatal("zero-value WebhookSender.IsConfigured() should be false")
		}
	})

	t.Run("configured sender returns true", func(t *testing.T) {
		w := NewWebhookSender("http://example.com/hook")
		if !w.IsConfigured() {
			t.Fatal("configured WebhookSender.IsConfigured() should be true")
		}
	})
}

// ==================== Send (early return paths) ====================

func TestWebhookSenderSend(t *testing.T) {
	ctx := context.Background()

	t.Run("not configured -> returns nil", func(t *testing.T) {
		w := &WebhookSender{} // zero value, url == ""
		err := w.Send(ctx, "some message")
		if err != nil {
			t.Fatalf("Send on not-configured sender should return nil, got %v", err)
		}
	})

	t.Run("empty string message on not-configured -> still nil", func(t *testing.T) {
		w := &WebhookSender{}
		err := w.Send(ctx, "")
		if err != nil {
			t.Fatalf("Send empty message on not-configured sender should return nil, got %v", err)
		}
	})
}

// ==================== SendNotification (early return paths) ====================

func TestWebhookSenderSendNotification(t *testing.T) {
	ctx := context.Background()

	t.Run("nil notification returns nil", func(t *testing.T) {
		w := &WebhookSender{} // url == "", not configured
		err := w.SendNotification(ctx, nil)
		if err != nil {
			t.Fatalf("SendNotification(nil) should return nil, got %v", err)
		}
	})

	t.Run("configured but nil notification -> still nil (short-circuit on n == nil)", func(t *testing.T) {
		// 通过 NewWebhookSender 创建已配置的 sender
		w := NewWebhookSender("http://example.com/hook")
		err := w.SendNotification(ctx, nil)
		if err != nil {
			t.Fatalf("SendNotification(nil) on configured sender should return nil, got %v", err)
		}
	})

	t.Run("not configured with non-nil notification -> still nil", func(t *testing.T) {
		w := &WebhookSender{}
		n := &Notification{Title: "test", Content: "hello"}
		err := w.SendNotification(ctx, n)
		if err != nil {
			t.Fatalf("SendNotification on not-configured sender should return nil, got %v", err)
		}
	})
}
