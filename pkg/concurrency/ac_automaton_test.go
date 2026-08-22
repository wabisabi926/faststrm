package concurrency_test

import (
	"testing"

	"github.com/wabisabi926/faststrm/pkg/concurrency"
)

func TestACBasicMatch(t *testing.T) {
	kw := []string{"sample", "trailer", "预览"}
	m := concurrency.NewStringMatcher(kw)
	cases := []struct {
		text   string
		hit    bool
		hitKW  string
	}{
		{"My.Sample.Movie.mkv", true, "sample"},
		{"Big.Trailer.2024.mp4", true, "trailer"},
		{"Final.Cut.iso", false, ""},
		{"预告片 预览版.mkv", true, "预览"},
		{"", false, ""},
		{"TraILER-caps.mkv", true, "trailer"},
	}
	for _, c := range cases {
		gotKW, ok := m.MatchAny(c.text)
		if ok != c.hit {
			t.Fatalf("text=%q want hit=%v got %v (kw=%q)", c.text, c.hit, ok, gotKW)
		}
		if c.hit && gotKW != c.hitKW {
			t.Fatalf("text=%q want kw=%q got %q", c.text, c.hitKW, gotKW)
		}
	}
}

func TestACEmptyAndNilCases(t *testing.T) {
	m := concurrency.NewStringMatcher(nil)
	if m.PatternCount() != 0 {
		t.Fatal("want 0 patterns")
	}
	if _, ok := m.MatchAny("anything"); ok {
		t.Fatal("no keyword must never hit")
	}
	m2 := concurrency.NewStringMatcher([]string{"", "  "}) // 空串和空白视为有效？旧逻辑只跳过空字符串
	_ = m2
}

func TestShouldUseACThreshold(t *testing.T) {
	if concurrency.ShouldUseAC(nil) {
		t.Fatal("nil -> false")
	}
	if concurrency.ShouldUseAC([]string{"a", "b", "c"}) {
		t.Fatal("< 8 -> false")
	}
	many := make([]string, 8)
	for i := range many {
		many[i] = "x"
	}
	if !concurrency.ShouldUseAC(many) {
		t.Fatal(">= 8 -> true")
	}
	// 空项不计入
	manyWithBlanks := append(many, "", "")
	if len(manyWithBlanks) == 10 && !concurrency.ShouldUseAC(manyWithBlanks) {
		t.Fatal("blanks must not count")
	}
}

func TestACLongerOverlapPatterns(t *testing.T) {
	kw := []string{"bad", "badder", "123"}
	m := concurrency.NewStringMatcher(kw)
	hitKW, ok := m.MatchAny("this is badder!")
	if !ok {
		t.Fatal("expect hit")
	}
	// 允许命中 bad 或 badder（按 trie 输出顺序）；只要是子串即可
	if hitKW != "bad" && hitKW != "badder" {
		t.Fatalf("unexpected kw %q", hitKW)
	}
}
