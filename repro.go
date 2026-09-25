//go:build ignore

// repro.go 是上游同步链路抓下来的复现脚本。
//
// 场景：一次全量同步把数据切成若干片段发出去，接收端按「增量推进校验值」
// 与「两段校验值合成」来对账。脚本把发出去的内容原样拼一遍、用
// hash/crc32 取整段校验值，再与 crcseg 的增量 / 合成结果逐条比对，
// 对不上的地方直接打出来。
//
// 跑法：
//
//	go run repro.go
//
// 全部对得上时退出码 0，有对不上的退出码 1。
package main

import (
	"fmt"
	"hash/crc32"
	"math/rand"
	"os"

	"crcseg"
)

var bad int

func report(name string, whole, got uint32) {
	if whole == got {
		fmt.Printf("一致  %-46s 整段=%#08x 分段=%#08x\n", name, whole, got)
		return
	}
	bad++
	fmt.Printf("对不上 %-45s 整段=%#08x 分段=%#08x\n", name, whole, got)
}

func split(rng *rand.Rand, data []byte) [][]byte {
	var segs [][]byte
	for i := 0; i < len(data); {
		n := 1 + rng.Intn(512)
		if i+n > len(data) {
			n = len(data) - i
		}
		segs = append(segs, data[i:i+n])
		i += n
	}
	return segs
}

func main() {
	rng := rand.New(rand.NewSource(20250925))

	// 1) 增量推进：分段喂进去，应该等于整段校验值。
	for _, total := range []int{1, 64, 4096, 1 << 16} {
		data := make([]byte, total)
		rng.Read(data)

		crc := uint32(0)
		for _, seg := range split(rng, data) {
			crc = crcseg.Update(crc, seg)
		}
		report(fmt.Sprintf("增量推进 %d 字节", total), crc32.ChecksumIEEE(data), crc)
	}

	// 2) 两段合成：只把两段的校验值给 Combine 传过去。
	for _, total := range []int{2, 1024, 1 << 15} {
		data := make([]byte, total)
		rng.Read(data)
		cut := total / 2

		a, b := data[:cut], data[cut:]
		got := crcseg.Combine(crcseg.Checksum(a), crcseg.Checksum(b), uint64(len(b)))
		report(fmt.Sprintf("两段合成 %d 字节（切在 %d）", total, cut), crc32.ChecksumIEEE(data), got)
	}

	// 3) 空片段：并进空片段不该改变校验值。
	seed := crcseg.Checksum([]byte("header/frame-7"))
	report("并进空片段", seed, crcseg.Update(seed, nil))
	report("合成空段", seed, crcseg.Combine(seed, 0, 0))

	// 4) 线上字节序：按小端约定打印。
	wire := crcseg.Marshal(0x01020304)
	fmt.Printf("Marshal(0x01020304) = % x\n", wire)

	fmt.Println()
	if bad == 0 {
		fmt.Println("复现脚本：全部一致")
		return
	}
	fmt.Printf("复现脚本：%d 处对不上\n", bad)
	os.Exit(1)
}
