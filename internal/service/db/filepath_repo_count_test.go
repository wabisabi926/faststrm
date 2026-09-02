package db

import (
	"strconv"
	"testing"
)

// 验证 CountFilesByPrefix：按 account + cloudPath 前缀统计 files 表记录数
func TestCountFilesByPrefix(t *testing.T) {
	db, cleanup := setupTmpDB(t)
	defer cleanup()

	// 准备数据：account=acc1 下 3 条 /电影/... + 2 条 /电视剧/...，account=acc2 下 1 条
	entries := []struct {
		account string
		path    string
	}{
		{"acc1", "/电影/a.mkv"},
		{"acc1", "/电影/sub/b.mkv"},
		{"acc1", "/电影/c.mkv"},
		{"acc1", "/电视剧/s01e01.mkv"},
		{"acc1", "/电视剧/s01e02.mkv"},
		{"acc2", "/电影/x.mkv"},
	}
	for i, e := range entries {
		entry := FilePathEntry{
			FileID:     strconv.Itoa(i + 1),
			Path:       e.path,
			FileName:   pathBase(e.path),
			ParentID:   "0",
			PickCode:   "pc" + strconv.Itoa(i+1),
			UpdateTime: 1700000000,
		}
		if err := UpsertFilePathEntry(db, e.account, entry); err != nil {
			t.Fatalf("Upsert %s %s: %v", e.account, e.path, err)
		}
	}

	// 1) nil db 安全降级，返回 0
	got, err := CountFilesByPrefix(nil, "acc1", "/电影")
	if err != nil {
		t.Fatalf("nil db CountFilesByPrefix err: %v", err)
	}
	if got != 0 {
		t.Errorf("nil db CountFilesByPrefix = %d, want 0", got)
	}

	// 2) 空 account 安全降级
	got, err = CountFilesByPrefix(db, "", "/电影")
	if err != nil {
		t.Fatalf("empty account err: %v", err)
	}
	if got != 0 {
		t.Errorf("empty account CountFilesByPrefix = %d, want 0", got)
	}

	// 3) 空 cloudPathPrefix：返回 acc1 全部记录数 = 5
	got, err = CountFilesByPrefix(db, "acc1", "")
	if err != nil {
		t.Fatalf("empty prefix err: %v", err)
	}
	if got != 5 {
		t.Errorf("acc1 全量 CountFilesByPrefix = %d, want 5", got)
	}

	// 4) 前缀 /电影（无尾斜杠）：应匹配 /电影/a.mkv /电影/sub/b.mkv /电影/c.mkv = 3
	got, err = CountFilesByPrefix(db, "acc1", "/电影")
	if err != nil {
		t.Fatalf("电影 prefix err: %v", err)
	}
	if got != 3 {
		t.Errorf("acc1 /电影 CountFilesByPrefix = %d, want 3", got)
	}

	// 5) 前缀 /电视剧/（带尾斜杠）：匹配 s01e01 + s01e02 = 2
	got, err = CountFilesByPrefix(db, "acc1", "/电视剧/")
	if err != nil {
		t.Fatalf("电视剧 prefix err: %v", err)
	}
	if got != 2 {
		t.Errorf("acc1 /电视剧/ CountFilesByPrefix = %d, want 2", got)
	}

	// 6) 不存在的前缀：返回 0
	got, err = CountFilesByPrefix(db, "acc1", "/不存在")
	if err != nil {
		t.Fatalf("nonexistent prefix err: %v", err)
	}
	if got != 0 {
		t.Errorf("nonexistent prefix CountFilesByPrefix = %d, want 0", got)
	}

	// 7) 跨 account 隔离：acc2 下 /电影 只有 1 条
	got, err = CountFilesByPrefix(db, "acc2", "/电影")
	if err != nil {
		t.Fatalf("acc2 电影 err: %v", err)
	}
	if got != 1 {
		t.Errorf("acc2 /电影 CountFilesByPrefix = %d, want 1", got)
	}
}

// pathBase 提取路径最后一段（避免引入 filepath 包）
func pathBase(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
