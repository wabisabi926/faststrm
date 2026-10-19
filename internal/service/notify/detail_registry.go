package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// Telegram 的 callback_data 上限是 64 字节。云端路径大多是中文（3 字节/字），
// 直接拼进 callback_data 必然超限，sendMessage 会返回 400 BUTTON_DATA_INVALID，
// 整条通知降级成纯文本、按钮丢失。
//
// 这里用「路径哈希 → 短 ID」做一层映射：callback_data 只带 16 个字符，
// 展示详情时再换回完整路径。哈希是确定性的，同一条通知重复发送也稳定。
//
// 存储策略：容量 + TTL 双重约束的 LRU（touch 式）。
// 旧实现「超过 4096 条整体清空」会让所有旧消息的「查看详情」同时失效；
// LRU 按最近使用淘汰，且 Lookup 会刷新 lastSeen，正在被点按钮的消息不会丢。
const (
	detailIDLen      = 16
	detailMaxEntries = 1024
	// detailEntryTTL 条目有效期。按钮挂在通知消息里，用户可能几天后才点
	detailEntryTTL = 7 * 24 * time.Hour
)

type detailEntry struct {
	path     string
	lastSeen time.Time
}

var (
	detailMu    sync.Mutex
	detailPaths = make(map[string]detailEntry, 64)
)

// RegisterDetailPath 为云端路径分配短 ID（供 detail_strm: 按钮使用）。
func RegisterDetailPath(path string) string {
	if path == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(path))
	id := hex.EncodeToString(sum[:detailIDLen/2])

	detailMu.Lock()
	defer detailMu.Unlock()
	if len(detailPaths) >= detailMaxEntries {
		detailEvictLocked()
	}
	detailPaths[id] = detailEntry{path: path, lastSeen: time.Now()}
	return id
}

// LookupDetailPath 用短 ID 换回云端路径（同时刷新该条目的活跃时间）。
func LookupDetailPath(id string) (string, bool) {
	detailMu.Lock()
	defer detailMu.Unlock()
	e, ok := detailPaths[id]
	if !ok {
		return "", false
	}
	if time.Since(e.lastSeen) > detailEntryTTL {
		delete(detailPaths, id)
		return "", false
	}
	e.lastSeen = time.Now()
	detailPaths[id] = e
	return e.path, true
}

// detailEvictLocked 淘汰策略：先删过期条目；仍满则按 lastSeen 淘汰最旧的 1/4。
// 调用方必须持有 detailMu。
func detailEvictLocked() {
	now := time.Now()
	for id, e := range detailPaths {
		if now.Sub(e.lastSeen) > detailEntryTTL {
			delete(detailPaths, id)
		}
	}
	if len(detailPaths) < detailMaxEntries {
		return
	}
	type kv struct {
		id   string
		seen time.Time
	}
	entries := make([]kv, 0, len(detailPaths))
	for id, e := range detailPaths {
		entries = append(entries, kv{id, e.lastSeen})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].seen.Before(entries[j].seen) })
	victims := len(entries) / 4
	if victims < 1 {
		victims = 1
	}
	for i := 0; i < victims; i++ {
		delete(detailPaths, entries[i].id)
	}
}

// DetailRegistryLen 当前注册表条目数（测试用）
func DetailRegistryLen() int {
	detailMu.Lock()
	defer detailMu.Unlock()
	return len(detailPaths)
}
