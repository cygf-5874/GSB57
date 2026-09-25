package crcseg

import (
	"bytes"
	"math/rand"
	"testing"
)

// 本文件是 crcseg 的既有用例。
//
// 它们只覆盖 ≤4 字节的小片段：短向量、分段推进的可结合性、编解码往返与入参不被改写。
// 更大的分段、合成与线上字节序的同源性由 segcheck/ 负责核对。

func TestChecksumCheckValue(t *testing.T) {
	const want = 0xCBF43926 // CRC-32/ISO-HDLC 的标准 check 值
	if got := Checksum([]byte("123456789")); got != want {
		t.Fatalf("Checksum(\"123456789\") = %#08x，期望 %#08x", got, want)
	}
}

func TestChecksumEmptyIsZero(t *testing.T) {
	if got := Checksum(nil); got != 0 {
		t.Fatalf("Checksum(nil) = %#08x，期望 0", got)
	}
	if got := Checksum([]byte{}); got != 0 {
		t.Fatalf("Checksum([]byte{}) = %#08x，期望 0", got)
	}
}

func TestChecksumShortVectors(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint32
	}{
		{[]byte{0x00}, 0xD202EF8D},
		{[]byte{0x01}, 0xA505DF1B},
		{[]byte{0xFF}, 0xFF000000},
		{[]byte{0x80}, 0x3FBA6CAD},
		{[]byte{0x00, 0x00}, 0x41D912FF},
		{[]byte("a"), 0xE8B7BE43},
		{[]byte("ab"), 0x9E83486D},
		{[]byte("abc"), 0x352441C2},
		{[]byte{0xDE, 0xAD, 0xBE, 0xEF}, 0x7C9CA35A},
	}

	for _, tc := range cases {
		if got := Checksum(tc.in); got != tc.want {
			t.Fatalf("Checksum(% x) = %#08x，期望 %#08x", tc.in, got, tc.want)
		}
	}
}

func TestChecksumDoesNotModifyInput(t *testing.T) {
	buf := []byte("the quick brown fox jumps over the lazy dog")
	snapshot := append([]byte(nil), buf...)

	_ = Checksum(buf)

	if !bytes.Equal(buf, snapshot) {
		t.Fatalf("Checksum 改写了入参：%q -> %q", snapshot, buf)
	}
}

func TestUpdateComposesAcrossChunks(t *testing.T) {
	rng := rand.New(rand.NewSource(11))

	for i := 0; i < 200; i++ {
		seed := rng.Uint32() | 1
		a := randomBytes(rng, 1+rng.Intn(4))
		b := randomBytes(rng, 1+rng.Intn(4))

		chained := Update(Update(seed, a), b)
		joined := Update(seed, append(append([]byte(nil), a...), b...))

		if chained != joined {
			t.Fatalf("第 %d 例：Update(Update(%#08x, % x), % x) = %#08x，与一次性并进 % x 的 %#08x 不同",
				i, seed, a, b, chained, append(append([]byte(nil), a...), b...), joined)
		}
	}
}

func TestUpdateDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(12))

	for i := 0; i < 100; i++ {
		crc := rng.Uint32()
		chunk := randomBytes(rng, 1+rng.Intn(4))

		first := Update(crc, chunk)
		second := Update(crc, chunk)

		if first != second {
			t.Fatalf("第 %d 例：同样的 (crc, chunk) 两次得到 %#08x / %#08x", i, first, second)
		}
	}
}

func TestUpdateDoesNotModifyChunk(t *testing.T) {
	rng := rand.New(rand.NewSource(13))

	for i := 0; i < 100; i++ {
		chunk := randomBytes(rng, 1+rng.Intn(4))
		snapshot := append([]byte(nil), chunk...)

		_ = Update(rng.Uint32(), chunk)

		if !bytes.Equal(chunk, snapshot) {
			t.Fatalf("第 %d 例：Update 改写了 chunk，% x -> % x", i, snapshot, chunk)
		}
	}
}

func TestUpdateManySingleByteChunks(t *testing.T) {
	rng := rand.New(rand.NewSource(14))

	for i := 0; i < 100; i++ {
		seed := rng.Uint32() | 1
		data := randomBytes(rng, 3)

		crc := seed
		for _, b := range data {
			crc = Update(crc, []byte{b})
		}

		if want := Update(seed, data); crc != want {
			t.Fatalf("第 %d 例：逐字节推进得 %#08x，一次性并进 % x 得 %#08x", i, crc, data, want)
		}
	}
}

func TestCombineEmptySecondSegmentIsIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(15))

	for i := 0; i < 50; i++ {
		crc := rng.Uint32() | 1

		if got := Combine(crc, 0, 0); got != crc {
			t.Fatalf("第 %d 例：Combine(%#08x, 0, 0) = %#08x，期望 %#08x", i, crc, got, crc)
		}
	}
}

func TestCombineZeroChecksumsIsZero(t *testing.T) {
	if got := Combine(0, 0, 0); got != 0 {
		t.Fatalf("Combine(0, 0, 0) = %#08x，期望 0", got)
	}
}

func TestCombineDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(16))

	for i := 0; i < 100; i++ {
		crcA := rng.Uint32()
		crcB := rng.Uint32()
		lenB := uint64(1 + rng.Intn(4))

		first := Combine(crcA, crcB, lenB)
		second := Combine(crcA, crcB, lenB)

		if first != second {
			t.Fatalf("第 %d 例：同样的参数两次得到 %#08x / %#08x", i, first, second)
		}
	}
}

func TestMarshalRoundTripAndLength(t *testing.T) {
	rng := rand.New(rand.NewSource(17))

	for i := 0; i < 200; i++ {
		crc := rng.Uint32()

		wire := Marshal(crc)
		if len(wire) != 4 {
			t.Fatalf("第 %d 例：Marshal(%#08x) 产出 %d 字节，期望 4", i, crc, len(wire))
		}

		got, err := Unmarshal(wire)
		if err != nil {
			t.Fatalf("第 %d 例：Unmarshal(Marshal(%#08x)) 返回错误 %v", i, crc, err)
		}
		if got != crc {
			t.Fatalf("第 %d 例：往返后 %#08x != %#08x", i, got, crc)
		}
	}
}

func TestUnmarshalRejectsBadLength(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 5, 8, 16} {
		if _, err := Unmarshal(make([]byte, n)); err == nil {
			t.Fatalf("长度 %d 的输入应当返回错误，实际返回 nil", n)
		}
	}
}

func randomBytes(rng *rand.Rand, n int) []byte {
	buf := make([]byte, n)
	rng.Read(buf)
	return buf
}
