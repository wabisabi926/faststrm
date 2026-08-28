package concurrency

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ==================== P2-2 Aho-Corasick 多模式匹配 ====================
// 替代 shouldGenerateStrm 中 O(N·M) 的 strings.Contains 串联匹配，
// 黑名单条数 ≥ 8 或 最长关键词长度较大时显著提速。

// acNode 表示自动机的节点
type acNode struct {
	next    map[rune]*acNode
	fail    *acNode
	outputs []string // 到达此节点时命中的全部模式（原始关键词，大小写与输入一致）
	depth   int      // 到根的距离（最长出匹配上界，备用）
}

// StringMatcher 是 Aho-Corasick 不区分大小写的多模式匹配器。
// 构建一次可在多文本上并发只读使用。
type StringMatcher struct {
	root         *acNode
	patternCount int // 输入模式总数（用于 AC vs contains 的阈值判断）
}

// NewStringMatcher 从不区分大小写的一组关键词构建 AC 自动机。
// 空切片/全部空词都返回非 nil 但 MatchAny 直接返回无匹配。
func NewStringMatcher(keywords []string) *StringMatcher {
	root := &acNode{next: make(map[rune]*acNode)}
	validCount := 0

	// 1) Build trie
	for _, raw := range keywords {
		if raw == "" {
			continue
		}
		kw := strings.ToLower(raw)
		node := root
		for _, r := range kw {
			if node.next[r] == nil {
				node.next[r] = &acNode{next: make(map[rune]*acNode), depth: node.depth + runeLen(r)}
			}
			node = node.next[r]
		}
		// 只对同一个最长最终节点去重 outputs（同一关键词重复传）
		dup := false
		for _, out := range node.outputs {
			if out == raw {
				dup = true
				break
			}
		}
		if !dup {
			node.outputs = append(node.outputs, raw)
			validCount++
		}
	}

	// 2) Build failure links via BFS
	queue := make([]*acNode, 0, 32)
	for _, child := range root.next {
		child.fail = root
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for r, child := range cur.next {
			// 找失败链接上第一个存在 r 转移的祖先
			f := cur.fail
			for f != nil && f.next[r] == nil {
				f = f.fail
			}
			if f == nil {
				child.fail = root
			} else {
				child.fail = f.next[r]
				// 合并失败链上的 outputs（使每个节点 outputs 是"到此节点时的全部出匹配"）
				if len(child.fail.outputs) > 0 {
					merged := make([]string, 0, len(child.outputs)+len(child.fail.outputs))
					merged = append(merged, child.outputs...)
					merged = append(merged, child.fail.outputs...)
					child.outputs = merged
				}
			}
			queue = append(queue, child)
		}
	}
	return &StringMatcher{root: root, patternCount: validCount}
}

// PatternCount 返回有效模式总数（用于决定 AC vs contains 阈值）
func (m *StringMatcher) PatternCount() int { return m.patternCount }

// MatchAny 在 text 中执行 AC 匹配，返回 (首个命中的原关键词, 是否命中)。
// 匹配规则：不区分大小写，子串包含匹配（与旧 shouldGenerateStrm 的 strings.Contains 等价）。
func (m *StringMatcher) MatchAny(text string) (string, bool) {
	if m == nil || m.patternCount == 0 || m.root == nil || text == "" {
		return "", false
	}
	lower := toLower(text)
	node := m.root
	for _, r := range lower {
		// 失败跳转直到 root
		for node != m.root && node.next[r] == nil {
			node = node.fail
		}
		if nxt, ok := node.next[r]; ok {
			node = nxt
		} else { //nolint:staticcheck // SA9003: 空分支为有意设计，保持 root 不变
			// node == root 且 root 无此转移：保持 root（已在上面的失败跳转循环中处理）
		}
		if len(node.outputs) > 0 {
			return node.outputs[0], true
		}
	}
	return "", false
}

// toLower 做 strings.ToLower，但对常见 ASCII 路径文本更快（无额外分配的兜底保留 strings.ToLower）
func toLower(s string) string {
	// 快速路径：检查是否需要 unicode 降级
	needUnicode := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			needUnicode = true
			break
		}
	}
	if !needUnicode {
		// 纯 ASCII：原地
		var b strings.Builder
		b.Grow(len(s))
		for i := 0; i < len(s); i++ {
			c := s[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			b.WriteByte(c)
		}
		return b.String()
	}
	return strings.ToLower(s)
}

// runeLen 估计 rune 的 utf8 字节数（仅用于 depth，无需精确）
func runeLen(r rune) int {
	if r < 128 {
		return 1
	}
	return utf8.RuneLen(r)
}

// ACThreshold 决定何时启用 AC 自动机（黑名单 ≥ 此值才构建）。
// 暴露给调用方，便于未来调优；默认 8。
const ACThreshold = 8

// ShouldUseAC 是一个便利判断：len(blacklist) >= ACThreshold 才启用自动机。
// 少于此阈值时 contains 更快（常数项低）。
func ShouldUseAC(blacklist []string) bool {
	n := 0
	for _, s := range blacklist {
		if s != "" {
			n++
		}
	}
	return n >= ACThreshold
}

// ==========================================================================
// Unicode 占位（仅规避 unused import "unicode" 警告；未来可做 NFKC 规范化）
var _ = unicode.IsLetter
