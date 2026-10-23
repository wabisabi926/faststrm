package client115

import (
	"context"
	"strings"
	"testing"
)

func TestFetchAllFsPages_CountDrivenShortPages(t *testing.T) {
	var offsets []int
	data, count, err := FetchAllFsPages(context.Background(), 1000, func(_ context.Context, offset, limit int) ([]FsFileEntry, int, error) {
		offsets = append(offsets, offset)
		if offset >= 1500 {
			return nil, 1500, nil
		}
		n := 500
		if offset+500 > 1500 {
			n = 1500 - offset
		}
		out := make([]FsFileEntry, n)
		for i := range out {
			out[i] = FsFileEntry{FID: float64(offset + i + 1), Name: "f"}
		}
		return out, 1500, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1500 || count != 1500 {
		t.Fatalf("got %d entries count=%d, want 1500", len(data), count)
	}
	if len(offsets) != 3 {
		t.Fatalf("expected 3 pages (0,500,1000), got offsets=%v", offsets)
	}
}

func TestFetchAllFsPages_IncompleteReturnsError(t *testing.T) {
	_, _, err := FetchAllFsPages(context.Background(), 1000, func(_ context.Context, offset, _ int) ([]FsFileEntry, int, error) {
		if offset == 0 {
			return []FsFileEntry{{Name: "a"}}, 1500, nil
		}
		return nil, 1500, nil
	})
	if err == nil || !strings.Contains(err.Error(), "分页不完整") {
		t.Fatalf("want incomplete error, got %v", err)
	}
}

func TestFetchAllFsPages_CountMissingUsesEmptyPage(t *testing.T) {
	calls := 0
	data, _, err := FetchAllFsPages(context.Background(), 1000, func(_ context.Context, offset, _ int) ([]FsFileEntry, int, error) {
		calls++
		if offset == 0 {
			return []FsFileEntry{{Name: "a"}}, 0, nil
		}
		return nil, 0, nil
	})
	if err != nil || len(data) != 1 || calls != 2 {
		t.Fatalf("data=%d calls=%d err=%v", len(data), calls, err)
	}
}

func TestFetchAllFsPages_DetectsRepeatedPage(t *testing.T) {
	_, _, err := FetchAllFsPages(context.Background(), 1000, func(_ context.Context, _ int, _ int) ([]FsFileEntry, int, error) {
		return []FsFileEntry{{Name: "same"}}, 0, nil
	})
	if err == nil || !strings.Contains(err.Error(), "分页重复") {
		t.Fatalf("want repeated page error, got %v", err)
	}
}
