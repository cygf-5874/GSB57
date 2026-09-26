// Package crcseg 提供 CRC-32/IEEE 的分段校验与合成能力。
//
// 面向分段传输的对账场景：发送端逐段推进校验值，接收端只拿到每一段的
// 校验值与长度，就要能算出拼接之后的校验值 —— 双方都不需要保留原始数据。
// 判据与实现口径以 README「对外保证」一节为准。
package crcseg

import (
	"encoding/binary"
	"hash/crc32"
)

// Poly 是 CRC-32/IEEE 的多项式（反射表示），值为 0xEDB88320。
//
// 这是本题写死的组合：多项式、初值 0xFFFFFFFF、输入输出反射、终值掩码 0xFFFFFFFF。
// 任何一段的校验值都必须是这套参数下的结果，不许换多项式。
const Poly uint32 = 0xEDB88320

// Checksum 返回 data 的 CRC-32/IEEE 校验值，与 hash/crc32.ChecksumIEEE 逐位一致。
func Checksum(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

// Update 把一个片段的字节并进已有的校验值 crc，返回新的校验值。
//
// crc 的取值约定与 Checksum 的返回值同一套：以 Checksum(第一段) 为起点，
// 依次把后续片段并进来，结果必须等于对拼接后的整段调用 Checksum。
func Update(crc uint32, chunk []byte) uint32 {
	return crc32.Update(crc, crc32.IEEETable, chunk)
}

// Combine 用两段的校验值推出拼接之后的校验值。
//
//	crcA —— 前一段（长度不限）的校验值
//	crcB —— 后一段的校验值
//	lenB —— 后一段的字节长度
//
// 返回值必须等于 Checksum(前一段 || 后一段)，且实现不得依赖前一段的原始数据。
//
// 算法与 zlib 的 crc32_combine 相同：把「补一个零比特」看成 GF(2) 上的
// 32x32 线性算子，反复平方得到「补 2^k 个零字节」的算子，再按 lenB 的
// 二进制位逐个作用到 crcA 上。lenB 是 uint64，全程按多项式模运算处理。
func Combine(crcA, crcB uint32, lenB uint64) uint32 {
	var odd, even [32]uint32

	// 补一个零比特的算子。
	odd[0] = Poly
	row := uint32(1)
	for n := 1; n < 32; n++ {
		odd[n] = row
		row <<= 1
	}

	// 补两个、四个零比特的算子。
	gf2Square(&even, &odd)
	gf2Square(&odd, &even)

	// 按 lenB 的每个比特，把「补 2^k 个零字节」的算子作用到 crcA 上。
	crc := crcA
	op, next := &odd, &even
	for lenB != 0 {
		gf2Square(next, op)
		if lenB&1 != 0 {
			crc = gf2Times(next, crc)
		}
		lenB >>= 1
		op, next = next, op
	}
	return crc ^ crcB
}

// gf2Times 返回 32x32 的 GF(2) 矩阵 mat 与向量 vec 的乘积。
func gf2Times(mat *[32]uint32, vec uint32) uint32 {
	var sum uint32
	for i := 0; vec != 0; i++ {
		if vec&1 != 0 {
			sum ^= mat[i]
		}
		vec >>= 1
	}
	return sum
}

// gf2Square 把 mat 的平方（算子自身复合一次）写入 square。
func gf2Square(square, mat *[32]uint32) {
	for n := 0; n < 32; n++ {
		square[n] = gf2Times(mat, mat[n])
	}
}

// Marshal 把校验值编码成线上传输的 4 个字节。
func Marshal(crc uint32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, crc)
	return out
}

// Unmarshal 从 Marshal 产出的 4 个字节里解出校验值；长度不是 4 时返回 ErrBadLength。
func Unmarshal(b []byte) (uint32, error) {
	if len(b) != 4 {
		return 0, ErrBadLength
	}
	return binary.LittleEndian.Uint32(b), nil
}
