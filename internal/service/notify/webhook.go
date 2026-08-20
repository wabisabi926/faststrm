package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

type WebhookSender struct {
	url    string
	client *http.Client
}

func NewWebhookSender(url string) *WebhookSender {
	if url == "" {
		return nil
	}
	return &WebhookSender{
		url: url,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *WebhookSender) IsConfigured() bool {
	return w != nil && w.url != ""
}

type webhookPayload struct {
	Title     string            `json:"title,omitempty"`
	Content   string            `json:"content"`
	Type      string            `json:"type,omitempty"`
	Priority  string            `json:"priority,omitempty"`
	Timestamp string            `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func (w *WebhookSender) Send(ctx context.Context, message string) error {
	if !w.IsConfigured() {
		return nil
	}
	payload := webhookPayload{
		Content:   message,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	return w.post(ctx, payload)
}

func (w *WebhookSender) SendNotification(ctx context.Context, n *Notification) error {
	if !w.IsConfigured() || n == nil {
		return nil
	}
	payload := webhookPayload{
		Title:     n.Title,
		Content:   n.Content,
		Type:      string(n.Type),
		Priority:  string(n.Priority),
		Timestamp: n.Timestamp.Format(time.RFC3339),
		Metadata:  n.Metadata,
	}
	return w.post(ctx, payload)
}

func (w *WebhookSender) post(ctx context.Context, payload webhookPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	logger.S().Debugf("[Webhook] Sent successfully (status=%d)", resp.StatusCode)
	return nil
}