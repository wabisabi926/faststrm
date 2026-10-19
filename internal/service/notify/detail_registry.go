package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Telegram 的 callback_data 上限是 64 字节。云端路径大多是中文（3 字节/字），
// 直接拼进 callback_data 必然超限，sendMessage 会返回 400 BUTTON_DATA_INVALID，
// 整条通知降级成纯文本、按钮丢失。
//
// 这里用「路径哈希 → 短 ID」做一层映射：callback_data 只带 16 个字符，
// 展示详情时再换回完整路径。哈希是确定性的，同一条通知重复发送也稳定。
const (
	detailIDLen      = 16
	detailRegistryCa = 4096
)

var (
	detailMu    sync.RWMutex
	detailPaths = make(map[string]string, 64)
)

// RegisterDetailPath 为云端路径分配短 ID（供 detail_strm: 按钮使用）。
func RegisterDetailPath(path string) string {
	if path == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(path))
	id := hex.EncodeToString(sum[:detailIDLen/2])

	detailMu.Lock()
	if len(detailPaths) >= detailRegistryCa {
		// 简单容量保护：长期运行不无限增长；清空后旧按钮会回退成「已过期」
		detailPaths = make(map[string]string, 64)
	}
	detailPaths[id] = path
	detailMu.Unlock()
	return id
}

// LookupDetailPath 用短 ID 换回云端路径。
func LookupDetailPath(id string) (string, bool) {
	detailMu.RLock()
	p, ok := detailPaths[id]
	detailMu.RUnlock()
	return p, ok
}
