package emby

import (
	"testing"
	"time"
)

// === DefaultRefreshOptions ===

func TestDefaultRefreshOptions(t *testing.T) {
	opts := DefaultRefreshOptions()
	if opts == nil {
		t.Fatal("should not be nil")
	}
	if !opts.Recursive {
		t.Fatal("Recursive should be true")
	}
	if opts.MetadataMode != "FullRefresh" {
		t.Fatalf("MetadataMode: got %q, want %q", opts.MetadataMode, "FullRefresh")
	}
	if opts.ImageMode != "FullRefresh" {
		t.Fatalf("ImageMode: got %q, want %q", opts.ImageMode, "FullRefresh")
	}
}

// === buildPlaybackCacheKey ===

func TestBuildPlaybackCacheKey(t *testing.T) {
	n := &Notifier{}

	t.Run("full event", func(t *testing.T) {
		ev := WebhookEvent{
			Event:      "play.start",
			DeviceName: "iPhone",
			Client:     "Emby Web",
			User:       &UserInfo{ID: "user1", Name: "Alice"},
			Item:       &ItemInfo{ID: "item1", Name: "Movie", Type: "Movie"},
		}
		key := n.buildPlaybackCacheKey(ev)
		want := "user1_Movie_Movie_play.start_iPhone_Emby Web"
		if key != want {
			t.Fatalf("got %q, want %q", key, want)
		}
	})

	t.Run("nil user and item", func(t *testing.T) {
		ev := WebhookEvent{
			Event:      "play.stop",
			DeviceName: "Android",
			Client:     "Emby Mobile",
		}
		key := n.buildPlaybackCacheKey(ev)
		want := "___play.stop_Android_Emby Mobile"
		if key != want {
			t.Fatalf("got %q, want %q", key, want)
		}
	})

	t.Run("different events produce different keys", func(t *testing.T) {
		ev1 := WebhookEvent{Event: "play.start", User: &UserInfo{ID: "u1"}, Item: &ItemInfo{Type: "Movie", Name: "A"}}
		ev2 := WebhookEvent{Event: "play.stop", User: &UserInfo{ID: "u1"}, Item: &ItemInfo{Type: "Movie", Name: "A"}}
		k1 := n.buildPlaybackCacheKey(ev1)
		k2 := n.buildPlaybackCacheKey(ev2)
		if k1 == k2 {
			t.Fatal("different events should produce different keys")
		}
	})
}

// === isPlaybackDuplicate ===

func TestIsPlaybackDuplicate(t *testing.T) {
	n := &Notifier{
		playbackCache: make(map[string]time.Time),
	}

	t.Run("first call returns false", func(t *testing.T) {
		ret := n.isPlaybackDuplicate("key1")
		if ret {
			t.Fatal("first call should return false (not duplicate)")
		}
	})

	t.Run("second call within window returns true", func(t *testing.T) {
		// First call marks it
		n.isPlaybackDuplicate("key2")
		// Second call immediately should be duplicate
		ret := n.isPlaybackDuplicate("key2")
		if !ret {
			t.Fatal("second call within dedup window should return true (duplicate)")
		}
	})

	t.Run("different keys are independent", func(t *testing.T) {
		n.isPlaybackDuplicate("keyA")
		ret := n.isPlaybackDuplicate("keyB")
		if ret {
			t.Fatal("different key should not be duplicate")
		}
	})
}

// === mergeDetail ===

func TestMergeDetail(t *testing.T) {
	t.Run("nil override", func(t *testing.T) {
		base := &ItemDetail{Name: "base"}
		mergeDetail(base, nil)
		if base.Name != "base" {
			t.Fatal("should not change base when override is nil")
		}
	})

	t.Run("override non-zero fields", func(t *testing.T) {
		base := &ItemDetail{
			Name:           "base",
			Overview:       "base overview",
			Genres:         []string{"Drama"},
			ProductionYear: 2020,
		}
		override := &ItemDetail{
			People:          []Person{{Name: "Director", Type: "Director"}},
			CommunityRating: 8.5,
			Overview:        "new overview",
			Genres:          []string{"Action"},
			ProductionYear:  2023,
			ImageTags:       map[string]string{"Primary": "abc"},
		}
		mergeDetail(base, override)
		if base.People[0].Name != "Director" {
			t.Fatal("People should be overridden")
		}
		if base.CommunityRating != 8.5 {
			t.Fatal("CommunityRating should be overridden")
		}
		if base.Overview != "new overview" {
			t.Fatal("Overview should be overridden")
		}
		if base.Genres[0] != "Action" {
			t.Fatal("Genres should be overridden")
		}
		if base.ProductionYear != 2023 {
			t.Fatal("ProductionYear should be overridden")
		}
		if base.ImageTags["Primary"] != "abc" {
			t.Fatal("ImageTags should be overridden")
		}
	})

	t.Run("override zero values don't replace base", func(t *testing.T) {
		base := &ItemDetail{
			Name:            "base",
			Overview:        "base overview",
			ProductionYear:  2020,
			CommunityRating: 7.0,
		}
		override := &ItemDetail{
			Name:            "override",
			Overview:        "", // empty, should NOT override
			ProductionYear:  0,  // zero, should NOT override
			CommunityRating: 0,  // zero, should NOT override
		}
		mergeDetail(base, override)
		if base.Overview != "base overview" {
			t.Fatal("empty Overview should not override")
		}
		if base.ProductionYear != 2020 {
			t.Fatal("zero ProductionYear should not override")
		}
		if base.CommunityRating != 7.0 {
			t.Fatal("zero CommunityRating should not override")
		}
	})
}

// === itemInfoToDetail ===

func TestItemInfoToDetail(t *testing.T) {
	item := ItemInfo{
		ID:                "123",
		Name:              "Test Movie",
		Type:              "Movie",
		SeriesName:        "Test Series",
		SeasonName:        "Season 1",
		ParentIndexNumber: 1,
		IndexNumber:       2,
		ProductionYear:    2023,
		Genres:            []string{"Action", "Drama"},
		Overview:          "A test movie overview",
		ImageTags:         map[string]string{"Primary": "tag123"},
	}

	detail := itemInfoToDetail(item)

	if detail.ID != "123" {
		t.Fatalf("ID: got %q, want %q", detail.ID, "123")
	}
	if detail.Name != "Test Movie" {
		t.Fatalf("Name: got %q, want %q", detail.Name, "Test Movie")
	}
	if detail.Type != "Movie" {
		t.Fatalf("Type: got %q, want %q", detail.Type, "Movie")
	}
	if detail.SeriesName != "Test Series" {
		t.Fatalf("SeriesName: got %q, want %q", detail.SeriesName, "Test Series")
	}
	if detail.ProductionYear != 2023 {
		t.Fatalf("ProductionYear: got %d, want %d", detail.ProductionYear, 2023)
	}
	if len(detail.Genres) != 2 || detail.Genres[0] != "Action" {
		t.Fatalf("Genres: got %v", detail.Genres)
	}
	if detail.Overview != "A test movie overview" {
		t.Fatalf("Overview: got %q", detail.Overview)
	}
	if detail.ImageTags["Primary"] != "tag123" {
		t.Fatalf("ImageTags: got %v", detail.ImageTags)
	}
}
