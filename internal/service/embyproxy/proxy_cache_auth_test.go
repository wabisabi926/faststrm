package embyproxy

import "testing"

func TestStrmSourcesCacheIsolatesAuthHeaders(t *testing.T) {
	p := &Proxy{strmSourcesCache: make(map[strmCacheKey]strmCacheEntry)}
	sources := map[string]strmSourceMeta{"s1": {path: "/x.strm"}}
	p.cacheStrmSources("item-1", "auth-a", sources)

	if _, ok := p.getCachedStrmSources("item-1", "auth-b"); ok {
		t.Fatal("cache must not be reused across different auth/header hashes")
	}
	if _, ok := p.getCachedStrmSources("item-1", "auth-a"); !ok {
		t.Fatal("cache should be available for the same auth/header hash")
	}
}
