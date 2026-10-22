package strm

import (
	"fmt"
	"testing"
	"time"
)

func TestComputeCdnURLCacheExpires(t *testing.T) {
	now := time.Now()
	ttl := 5 * time.Minute
	tests := []struct {
		name       string
		url        string
		wantAfter  time.Duration // 期望过期时间相对 now 的下限
		wantBefore time.Duration // 期望过期时间相对 now 的上限
	}{
		{name: "far expiry capped by ttl", url: fmt.Sprintf("https://cdn/x?t=%d", now.Add(2*time.Hour).Unix()), wantAfter: 4*time.Minute + 50*time.Second, wantBefore: 5*time.Minute + time.Second},
		{name: "near expiry keeps remaining minus safety", url: fmt.Sprintf("https://cdn/x?t=%d", now.Add(4*time.Minute).Unix()), wantAfter: 2*time.Minute + 50*time.Second, wantBefore: 3*time.Minute + time.Second},
		{name: "too close is not cacheable", url: fmt.Sprintf("https://cdn/x?t=%d", now.Add(30*time.Second).Unix()), wantAfter: -5 * time.Second, wantBefore: 0},
		{name: "expired is not cacheable", url: fmt.Sprintf("https://cdn/x?t=%d", now.Add(-time.Minute).Unix()), wantAfter: -5 * time.Second, wantBefore: 0},
		{name: "missing t uses default ttl", url: "https://cdn/x", wantAfter: 4*time.Minute + 50*time.Second, wantBefore: 5*time.Minute + time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeCdnURLCacheExpires(tt.url, ttl)
			delta := got.Sub(time.Now())
			if delta < tt.wantAfter || delta > tt.wantBefore {
				t.Fatalf("expires delta = %v, want [%v, %v]", delta, tt.wantAfter, tt.wantBefore)
			}
		})
	}
}
