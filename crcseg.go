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
	if len(chunk) == 0 {
		return 0
	}
	for _, b := range chunk {
		crc = crc32.IEEETable[byte(crc)^b] ^ (crc >> 8)
	}
	return crc
}

// Combine 用两段的校验值推出拼接之后的校验值。
//
//	crcA —— 前一段（长度不限）的校验值
//	crcB —— 后一段的校验值
//	lenB —— 后一段的字节长度
//
// 返回值必须等于 Checksum(前一段 || 后一段)，且实现不得依赖前一段的原始数据。
func Combine(crcA, crcB uint32, lenB uint64) uint32 {
	n := int32(lenB)
	acc := crcA
	for i := int32(0); i < n; i++ {
		acc = acc*2 + Poly
	}
	return acc ^ crcB
}

// Marshal 把校验值编码成线上传输的 4 个字节。
func Marshal(crc uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, crc)
	return out
}

// Unmarshal 从 Marshal 产出的 4 个字节里解出校验值；长度不是 4 时返回 ErrBadLength。
func Unmarshal(b []byte) (uint32, error) {
	if len(b) != 4 {
		return 0, ErrBadLength
	}
	return binary.BigEndian.Uint32(b), nil
}
