// Package crypto115 实现 115 网盘 API 通信的自定义加解密算法
// 与 frontend/src/lib/115crypto.ts 严格逐字节对齐
//
// 算法流程:
//   加密: plain → XOR[8d,a5,a5,8d] → reverse → XOR[78,06,ad,4c,33,86,5d,18,4c,01,3f,46] → PKCS1 v1.5 padding → RSA(m=RSA_e, e=RSA_n) → base64
//   解密: base64 → RSA(m=RSA_e, e=RSA_n) → 去掉 PKCS1 padding → 前16字节→genKey→XOR 剩余部分 → reverse → XOR[8d,a5,a5,8d] → utf-8
package crypto115

import (
	"encoding/base64"
	"errors"
	"math/big"
)

// ========== 硬编码常量（与 TS G_kts / RSA_* 完全一致）==========

// G_kts 密钥表，逐字节拷贝自 115crypto.ts
var G_kts = []byte{
	0xf0, 0xe5, 0x69, 0xae, 0xbf, 0xdc, 0xbf, 0x8a,
	0x1a, 0x45, 0xe8, 0xbe, 0x7d, 0xa6, 0x73, 0xb8,
	0xde, 0x8f, 0xe7, 0xc4, 0x45, 0xda, 0x86, 0xc4,
	0x9b, 0x64, 0x8b, 0x14, 0x6a, 0xb4, 0xf1, 0xaa,
	0x38, 0x01, 0x35, 0x9e, 0x26, 0x69, 0x2c, 0x86,
	0x00, 0x6b, 0x4f, 0xa5, 0x36, 0x34, 0x62, 0xa6,
	0x2a, 0x96, 0x68, 0x18, 0xf2, 0x4a, 0xfd, 0xbd,
	0x6b, 0x97, 0x8f, 0x4d, 0x8f, 0x89, 0x13, 0xb7,
	0x6c, 0x8e, 0x93, 0xed, 0x0e, 0x0d, 0x48, 0x3e,
	0xd7, 0x2f, 0x88, 0xd8, 0xfe, 0xfe, 0x7e, 0x86,
	0x50, 0x95, 0x4f, 0xd1, 0xeb, 0x83, 0x26, 0x34,
	0xdb, 0x66, 0x7b, 0x9c, 0x7e, 0x9d, 0x7a, 0x81,
	0x32, 0xea, 0xb6, 0x33, 0xde, 0x3a, 0xa9, 0x59,
	0x34, 0x66, 0x3b, 0xaa, 0xba, 0x81, 0x60, 0x48,
	0xb9, 0xd5, 0x81, 0x9c, 0xf8, 0x6c, 0x84, 0x77,
	0xff, 0x54, 0x78, 0x26, 0x5f, 0xbe, 0xe8, 0x1e,
	0x36, 0x9f, 0x34, 0x80, 0x5c, 0x45, 0x2c, 0x9b,
	0x76, 0xd5, 0x1b, 0x8f, 0xcc, 0xc3, 0xb8, 0xf5,
}

// RSA 公钥参数（注意：TS 中 RSA_e 是模，RSA_n 是指数 0x10001）
var (
	RSAe, _ = new(big.Int).SetString("8686980c0f5a24c4b9d43020cd2c22703ff3f450756529058b1cf88f09b8602136477198a6e2683149659bd122c33592fdb5ad47944ad1ea4d36c6b172aad6338c3bb6ac6227502d010993ac967d1aef00f0c8e038de2e4d3bc2ec368af2e9f10a6f1eda4f7262f136420c07c331b871bf139f74f3010e3c4fe57df3afb71683", 16)
	RSAn    = big.NewInt(0x10001)
)

// 固定 XOR 密钥（从 TS 源码提取）
var (
	xorKey4Bytes  = []byte{0x8d, 0xa5, 0xa5, 0x8d}
	xorKey12Bytes = []byte{0x78, 0x06, 0xad, 0x4c, 0x33, 0x86, 0x5d, 0x18, 0x4c, 0x01, 0x3f, 0x46}
)

// PKCS1 v1.5 填充块大小（RSA 1024bit = 128 字节）
const rsaBlockSize = 128
const rsaEncChunk = 117 // 128 - 11 (PKCS1 最小填充)

// ========== 对外 API ==========

// Encrypt 对字符串或二进制数据执行 115 自定义加密
// 返回 base64 编码的密文字符串
func Encrypt(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty data")
	}

	// 1. 双层 XOR + reverse，前置 16 字节 randKey
	// xorText = [16 rand bytes | XOR12(reverse(XOR4(data)))]
	innerXor := bytesXor(data, xorKey4Bytes)
	reverseBytes(innerXor)
	payload := bytesXor(innerXor, xorKey12Bytes)

	xorText := make([]byte, 16+len(payload))
	// 前 16 字节填入随机密钥（genKey 的输入）。
	// 注意：TS 版本没有显式设置前 16 字节，依赖未初始化内存 (new Uint8Array) 为 0，
	// 实际上 TS 代码 xorText.set(xor(...), 16) 前未填 xorText[0:16]，即默认为 0。
	// 为了兼容解密，我们必须使用一致的逻辑：前 16 字节加密时是随机 0（但实际上 TS 用 0 填充），
	// 但解密需要从 xorText[0:16] 来 genKey。为了对齐 TS 的输出，这里填 0。
	// 填 0 不影响安全性（因为真正的密钥是 RSA 私钥不在客户端）。
	for i := 0; i < 16; i++ {
		xorText[i] = 0
	}
	copy(xorText[16:], payload)

	// 2. 按 117 字节分块 RSA 加密，每块输出 128 字节
	numChunks := (len(xorText) + rsaEncChunk - 1) / rsaEncChunk
	cipherData := make([]byte, numChunks*rsaBlockSize)
	start := 0
	for l := 0; l < len(xorText); l += rsaEncChunk {
		r := l + rsaEncChunk
		if r > len(xorText) {
			r = len(xorText)
		}
		chunk := xorText[l:r]
		padded := padPKCS1v15(chunk, rsaBlockSize)
		m := new(big.Int).SetBytes(padded)
		c := bigPow(m, RSAn, RSAe)
		out := bigIntToBytes(c, rsaBlockSize)
		copy(cipherData[start:start+rsaBlockSize], out)
		start += rsaBlockSize
	}

	// 3. Base64 编码输出
	return base64.StdEncoding.EncodeToString(cipherData), nil
}

// EncryptString 加密字符串
func EncryptString(s string) (string, error) {
	return Encrypt([]byte(s))
}

// Decrypt 解密 115 自定义加密的密文（base64 格式）
func Decrypt(cipherBase64 string) (string, error) {
	cipherData, err := base64.StdEncoding.DecodeString(cipherBase64)
	if err != nil {
		return "", err
	}
	if len(cipherData)%rsaBlockSize != 0 {
		return "", errors.New("invalid cipher length")
	}

	var out []byte
	// 分块 RSA 解密
	// 对齐 p115cipher rsa_decrypt_with_pubkey:
	//   1. RSA 解密后转为最小字节数（不补前导0）
	//   2. 查找第一个 0x00 字节作为 PKCS1 分隔符
	//   3. 取分隔符之后的数据（不强制检查 0x00 0x02 前缀）
	for l := 0; l < len(cipherData); l += rsaBlockSize {
		r := l + rsaBlockSize
		chunk := cipherData[l:r]
		c := new(big.Int).SetBytes(chunk)
		m := bigPow(c, RSAn, RSAe)
		// 转为最小字节数（等价于 Python: to_bytes(p, (p.bit_length()+7)>>3)）
		b := bigIntToBytesMinimal(m)
		// 查找第一个 0x00 字节（PKCS1 v1.5 分隔符）
		zeroIdx := findByteFrom(b, 0, 0)
		if zeroIdx < 0 {
			return "", errors.New("invalid PKCS1 padding (no 0x00 separator)")
		}
		out = append(out, b[zeroIdx+1:]...)
	}

	dataArray := out
	if len(dataArray) < 16 {
		return "", errors.New("decrypted data too short (missing rand key)")
	}

	// 前 16 字节是 randKey → 派生 XOR 密钥（12 字节）
	randKey := dataArray[:16]
	keyL := genKey(randKey, 12)

	// XOR 16 之后的数据
	tmp := bytesXor(dataArray[16:], keyL)
	reverseBytes(tmp)

	// 再次 XOR 4 字节密钥
	plain := bytesXor(tmp, xorKey4Bytes)
	return string(plain), nil
}

// ========== 内部工具函数（与 TS 同名函数逐行对齐）==========

// genKey 对应 TS: genKey(randKey, skLen)
// 派生 XOR 密钥，skLen 通常为 12
func genKey(randKey []byte, skLen int) []byte {
	xorKey := make([]byte, skLen)
	length := skLen * (skLen - 1)
	index := 0
	for i := 0; i < skLen; i++ {
		x := (randKey[i] + G_kts[index]) & 0xff
		xorKey[i] = G_kts[length] ^ x
		length -= skLen
		index += skLen
	}
	return xorKey
}

// padPKCS1V1_5 对应 TS: padPkcs1V1_5(message)
//
//	TS 代码（关键对齐点）：
//	  buffer = new Uint8Array(128)          // 全部初始化为 0
//	  buffer.fill(0x02, 1, 127 - msg_len)  // [1, 127-msg_len) 区间填 0x02
//	  buffer.set(message, 128 - msg_len)   // 尾部设置消息
//
//	注意: 索引 (127 - msg_len) 不在 fill 范围内，它保持为 0 → 成为 PKCS1 分隔符 0x00。
//	      0x02 填充区间其实是 [1, 127-msg_len)，也就是 i 从 1 到 (127-msg_len-1)。
func padPKCS1v15(msg []byte, blockLen int) []byte {
	msgLen := len(msg)
	if msgLen > blockLen-11 {
		panic("message too long for PKCS1 v1.5 block")
	}
	buf := make([]byte, blockLen) // 全部初始化为 0

	fillEnd := (blockLen - 1) - msgLen // 127 - msg_len，fill 的结束位置（不包含）
	for i := 1; i < fillEnd; i++ {
		buf[i] = 0x02
	}
	// buf[fillEnd] 未被修改，保持为 0x00 → 分隔符
	copy(buf[blockLen-msgLen:], msg)
	return buf
}

// bigPow 模幂：(base^exponent) mod modulus
// 对应 TS: pow(base, exponent, modulus) 使用 big.Int
func bigPow(base, exponent, modulus *big.Int) *big.Int {
	if modulus.Cmp(big.NewInt(1)) == 0 {
		return big.NewInt(0)
	}
	result := big.NewInt(1)
	b := new(big.Int).Set(base)
	b.Mod(b, modulus)
	e := new(big.Int).Set(exponent)
	for e.Sign() > 0 {
		if e.Bit(0) == 1 {
			result.Mul(result, b)
			result.Mod(result, modulus)
		}
		e.Rsh(e, 1)
		b.Mul(b, b)
		b.Mod(b, modulus)
	}
	return result
}

// bytesXor 逐字节 XOR，src 和 key 可以长度不同
// 对应 TS: xor(src, key) + bytesXor(v1, v2)
func bytesXor(src, key []byte) []byte {
	if len(src) == 0 || len(key) == 0 {
		return append([]byte(nil), src...)
	}
	out := make([]byte, len(src))
	n := len(src) & 3
	if n > 0 {
		for i := 0; i < n; i++ {
			out[i] = src[i] ^ key[i%len(key)]
		}
	}
	// 对齐 key.length 步长处理剩余部分
	for i := n; i < len(src); i += len(key) {
		end := i + len(key)
		if end > len(src) {
			end = len(src)
		}
		for j := i; j < end; j++ {
			out[j] = src[j] ^ key[j-i]
		}
	}
	return out
}

// reverseBytes 原地反转字节切片
func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

// bigIntToBytes 将 big.Int 转为指定长度的大端字节数组（左侧补 0）
// 对应 TS: toBytes(value, length)
func bigIntToBytes(v *big.Int, length int) []byte {
	b := v.Bytes()
	if len(b) >= length {
		// 如果超出，截取尾部（保留最低 length 字节）
		return b[len(b)-length:]
	}
	out := make([]byte, length)
	copy(out[length-len(b):], b)
	return out
}

// bigIntToBytesMinimal 转为大端字节，无前导 0（除了 0 本身返回 [0x00]）
// 对应 TS: toBytes(value)（无 length 参数分支）
func bigIntToBytesMinimal(v *big.Int) []byte {
	if v.Sign() == 0 {
		return []byte{0}
	}
	return v.Bytes()
}

// findByteFrom 从 from 索引开始查找第一个匹配字节的索引
func findByteFrom(b []byte, v byte, from int) int {
	for i := from; i < len(b); i++ {
		if b[i] == v {
			return i
		}
	}
	return -1
}
