package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShouldRefreshUpstream(t *testing.T) {
	for _, status := range []int{401, 403, 404, 408, 429, 430, 500, 502, 599} {
		if !shouldRefreshUpstream(status) {
			t.Fatalf("status %d should refresh", status)
		}
	}
	for _, status := range []int{200, 206, 400, 416} {
		if shouldRefreshUpstream(status) {
			t.Fatalf("status %d should not refresh", status)
		}
	}
	if !isRetryableChunk(430) {
		t.Fatal("chunk 430 should be retryable")
	}
}

func TestFetchUpstreamRefreshesOnTransportError(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer good.Close()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	resp, err := fetchUpstreamWithRefresh(
		httptest.NewRecorder(), context.Background(), http.MethodGet,
		"http://127.0.0.1:1/closed", nil,
		func(context.Context) (string, error) { return good.URL, nil },
		timer,
	)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
