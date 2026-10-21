package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactTelegramErrorHidesBotToken(t *testing.T) {
	err := errors.New("Post https://api.telegram.org/bot123456789:AAF_fake-token_123/getUpdates: timeout")
	got := redactTelegramError(err)
	if strings.Contains(got, "AAF_fake-token_123") {
		t.Fatalf("token leaked in log: %s", got)
	}
	if !strings.Contains(got, "bot<redacted>") {
		t.Fatalf("redaction marker missing: %s", got)
	}
}

func TestIsPollingConflict(t *testing.T) {
	for _, msg := range []string{
		"Conflict: terminated by other getUpdates request",
		"telegram api error 409",
	} {
		if !isPollingConflict(errors.New(msg)) {
			t.Fatalf("should detect conflict: %s", msg)
		}
	}
	if isPollingConflict(errors.New("i/o timeout")) {
		t.Fatal("timeout must not be treated as conflict")
	}
}
