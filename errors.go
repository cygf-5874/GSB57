package crcseg

import "errors"

var (
	// ErrBadLength 表示 Unmarshal 收到的字节数不是 4。
	ErrBadLength = errors.New("crcseg: checksum field must be exactly 4 bytes")
)
