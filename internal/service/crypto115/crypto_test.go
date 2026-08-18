package crypto115

import (
	"bytes"
	"encoding/base64"
	"math/big"
	"testing"
)

// ========== 基础工具函数 ==========

func TestBigPowVector(t *testing.T) {
	base := big.NewInt(12345)
	exp := big.NewInt(67890)
	mod := big.NewInt(0x10001)
	got := bigPow(base, exp, mod)
	want := new(big.Int).Exp(base, exp, mod)
	if got.Cmp(want) != 0 {
		t.Fatalf("bigPow mismatch: got %s, want %s", got, want)
	}
}

func TestPadPKCS1v15(t *testing.T) {
	msg := []byte{0x01, 0x02, 0x03}
	got := padPKCS1v15(msg, 16)
	if len(got) != 16 {
		t.Fatalf("bad pad length: %d", len(got))
	}
	if got[0] != 0x00 || got[1] != 0x02 {
		t.Fatalf("bad PKCS1 header: %v", got[:2])
	}
	if !bytes.Equal(got[16-3:], msg) {
		t.Fatalf("message misaligned: tail=%v", got[16-3:])
	}
	// fillEnd = (blockLen-1) - msgLen = 15 - 3 = 12 → 分隔符在索引 12
	wantSep := 12
	if got[wantSep] != 0 {
		t.Fatalf("expected 0x00 separator at %d, got 0x%02x", wantSep, got[wantSep])
	}
	for i := 1; i < wantSep; i++ {
		if got[i] != 0x02 {
			t.Fatalf("expected 0x02 at index %d, got 0x%02x", i, got[i])
		}
	}
}

func TestBytesXor(t *testing.T) {
	a := []byte{0xff, 0x00, 0xaa, 0x55, 0x11, 0x22}
	k := []byte{0x01, 0x02}
	out := bytesXor(a, k)
	want := []byte{
		0xff ^ 0x01, 0x00 ^ 0x02,
		0xaa ^ 0x01, 0x55 ^ 0x02,
		0x11 ^ 0x01, 0x22 ^ 0x02,
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("bytesXor mismatch\ngot  %v\nwant %v", out, want)
	}
}

func TestReverseBytes(t *testing.T) {
	b := []byte{1, 2, 3, 4, 5}
	reverseBytes(b)
	want := []byte{5, 4, 3, 2, 1}
	if !bytes.Equal(b, want) {
		t.Fatalf("reverse wrong: %v", b)
	}
}

func TestBigIntToBytes(t *testing.T) {
	v := big.NewInt(0x010203)
	out := bigIntToBytes(v, 4)
	want := []byte{0, 0x01, 0x02, 0x03}
	if !bytes.Equal(out, want) {
		t.Fatalf("bigIntToBytes: %v vs %v", out, want)
	}
}

// TestGenKeyBounds skLen=12 不越界访问 G_kts（G_kts 长 144 字节，skLen=12 → index+=12 每次最大 index=12*11=132 < 144；length=skLen*(skLen-1)=132，每次减 12 也 ≥0）
func TestGenKeyBounds(t *testing.T) {
	randKey := make([]byte, 16)
	out := genKey(randKey, 12)
	if len(out) != 12 {
		t.Fatalf("genKey len: %d", len(out))
	}
	_ = G_kts[len(G_kts)-1] // 验证 G_kts 长度不 panic
}

// ========== Encrypt 格式与确定性测试 ==========

// TestEncryptDeterministic xorText 前 16 字节固定为 0（对齐 TS new Uint8Array 未初始化），
// 加密完全确定 → 多次调用一致
func TestEncryptDeterministic(t *testing.T) {
	plain := []byte("hello 115")
	enc1, err := Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	enc2, err := Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc1 != enc2 {
		t.Fatalf("encrypt not deterministic:\n  %s\n  %s", enc1, enc2)
	}
}

// TestEncryptBlockSize 多种输入长度输出符合：ceil(xorTextLen/117) * 128 字节
func TestEncryptBlockSize(t *testing.T) {
	for _, n := range []int{1, 100, 117, 118, 200, 300} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i + 1)
		}
		enc, err := Encrypt(data)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(enc)
		xorTextLen := 16 + n
		expectedChunks := (xorTextLen + rsaEncChunk - 1) / rsaEncChunk
		expectedBytes := expectedChunks * rsaBlockSize
		if len(raw) != expectedBytes {
			t.Fatalf("n=%d: got %d bytes encrypted, want %d (chunks=%d)",
				n, len(raw), expectedBytes, expectedChunks)
		}
	}
}

// TestEncryptString 字符串入口
func TestEncryptString(t *testing.T) {
	enc, err := EncryptString("ping")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw) != rsaBlockSize {
		t.Fatalf("bad enc output len=%d err=%v", len(raw), err)
	}
}
