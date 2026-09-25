// Command segcheck 是 crcseg 的固定验收程序。
//
// ⚠️ 不要修改本文件。它是判定「题目有没有做对」的依据；
// 修改它只会让判定失效，不会让实现变对。
//
// 10 个场景分四组：
//
//	update  3 个：与 hash/crc32 对拍 / 分段推进的可结合性 / 增量结果等于整段结果
//	combine 3 个：两段 / 三段 / 多段 的合成对拍卖
//	edge    2 个：空片段恒等元 / lenB 是 uint64（含超过 2^32 的长度）
//	wire    2 个：Marshal 的字节序 / 编解码往返与长度校验
//
// 用法：
//
//	go run ./segcheck                  # 跑全部场景
//	go run ./segcheck -only combine    # 只跑一组（--only 等价）
//	go run ./segcheck -list            # 列出场景
//
// 判据只调用被测库的校验/增量/合成/编解码接口：随机分段与整段结果对拍时，
// 期望值由本程序用 hash/crc32 对整段数据独立算出，被测实现只拿到片段与长度。
// 全程确定性（固定种子的 PRNG），不依赖墙钟与机器速度。
// 每个场景在独立子进程里跑，并有 25 秒看门狗，超时会转储全部 goroutine 栈。
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"hash/crc32"
	"math/rand"
	"os"
	"os/exec"
	"runtime/pprof"
	"strings"
	"time"

	"crcseg"
)

const (
	childTimeout = 25 * time.Second
	parentGrace  = 10 * time.Second
)

type scenario struct {
	Group string
	Name  string
	Run   func() error
}

var scenarios = []scenario{
	{"update", "UPD1_checksum_matches_stdlib", checksumMatchesStdlib},
	{"update", "UPD2_chunked_update_composition", chunkedUpdateComposition},
	{"update", "UPD3_incremental_equals_full", incrementalEqualsFull},
	{"combine", "CMB1_two_segments", combineTwoSegments},
	{"combine", "CMB2_three_segments", combineThreeSegments},
	{"combine", "CMB3_many_segments", combineManySegments},
	{"edge", "EDG1_empty_segment_identity", emptySegmentIdentity},
	{"edge", "EDG2_lenb_uint64_range", lenbUint64Range},
	{"wire", "WIR1_marshal_little_endian", marshalLittleEndian},
	{"wire", "WIR2_roundtrip_and_errors", roundtripAndErrors},
}

// ------------------------------------------------------------------- runner

func main() {
	only := flag.String("only", "", "只跑指定组：update / combine / edge / wire")
	name := flag.String("scenario", "", "内部使用：只跑单个场景")
	list := flag.Bool("list", false, "列出全部场景")
	flag.Parse()

	if *list {
		for _, sc := range scenarios {
			fmt.Printf("%-8s %s\n", sc.Group, sc.Name)
		}
		return
	}
	if *name != "" {
		os.Exit(runChild(*name))
	}
	os.Exit(runParent(*only))
}

func runChild(name string) int {
	var sc *scenario
	for i := range scenarios {
		if scenarios[i].Name == name {
			sc = &scenarios[i]
			break
		}
	}
	if sc == nil {
		fmt.Fprintf(os.Stderr, "未知场景 %q\n", name)
		return 2
	}

	watchdog := time.AfterFunc(childTimeout, func() {
		fmt.Fprintf(os.Stderr, "看门狗：场景 %s 超过 %s 仍未结束，下面是全部 goroutine 栈\n",
			sc.Name, childTimeout)
		_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
		os.Exit(3)
	})
	defer watchdog.Stop()

	if err := sc.Run(); err != nil {
		fmt.Printf("%s\n", err)
		return 1
	}
	fmt.Printf("OK\n")
	return 0
}

func runParent(only string) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Println("无法定位自身可执行文件:", err)
		return 1
	}

	groups := []string{"update", "combine", "edge", "wire"}
	if only != "" {
		found := false
		for _, g := range groups {
			if g == only {
				found = true
			}
		}
		if !found {
			fmt.Printf("未知分组 %q（可选：update / combine / edge / wire）\n", only)
			return 2
		}
		groups = []string{only}
	}

	total, passed := 0, 0
	for _, g := range groups {
		fmt.Printf("== 组 %s ==\n", g)
		for _, sc := range scenarios {
			if sc.Group != g {
				continue
			}
			total++

			ctx, cancel := context.WithTimeout(context.Background(), childTimeout+parentGrace)
			cmd := exec.CommandContext(ctx, exe, "-scenario", sc.Name)
			cmd.WaitDelay = 5 * time.Second
			out, runErr := cmd.CombinedOutput()
			cancel()

			text := strings.TrimSpace(string(out))
			if runErr == nil {
				passed++
				fmt.Printf("PASS %s/%s\n", sc.Group, sc.Name)
				continue
			}

			detail := firstLine(text)
			if detail == "" {
				detail = fmt.Sprintf("子进程未能给出结论（%v）", runErr)
			}
			fmt.Printf("FAIL %s/%s  %s\n", sc.Group, sc.Name, detail)
			for _, line := range tailLines(restLines(text), 10) {
				fmt.Printf("        | %s\n", line)
			}
		}
	}

	fmt.Println()
	fmt.Printf("结果：通过 %d/%d\n", passed, total)
	if passed != total {
		return 1
	}
	return 0
}

func firstLine(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

func restLines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return nil
	}
	return lines[1:]
}

func tailLines(lines []string, n int) []string {
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(l))
		}
	}
	if len(nonEmpty) > n {
		nonEmpty = append([]string{"…"}, nonEmpty[len(nonEmpty)-n:]...)
	}
	return nonEmpty
}

// mismatch 组装失败说明：固定件统一打印「期望=… 实际=…」。
func mismatch(want, got string) error {
	return fmt.Errorf("期望=%s 实际=%s", want, got)
}

// -------------------------------------------------------------------- tools

func randBytes(rng *rand.Rand, n int) []byte {
	buf := make([]byte, n)
	rng.Read(buf)
	return buf
}

func patterned(seed byte, n int) []byte {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(i*7+int(seed)*31) ^ seed
	}
	return buf
}

// ------------------------------------------------------------------- update

// UPD1：Checksum 必须与 hash/crc32.ChecksumIEEE 逐位一致（含空串与短向量）。
func checksumMatchesStdlib() error {
	vectors := [][]byte{
		nil,
		{},
		{0x00},
		{0xFF},
		{0x00, 0x00},
		[]byte("a"),
		[]byte("abcdefghij"),
		[]byte("123456789"),
		patterned(0x11, 4096),
	}

	for _, v := range vectors {
		want := crc32.ChecksumIEEE(v)
		if got := crcseg.Checksum(v); got != want {
			return mismatch(fmt.Sprintf("Checksum(%d 字节)=%#08x", len(v), want),
				fmt.Sprintf("%#08x", got))
		}
	}

	rng := rand.New(rand.NewSource(101))
	for i := 0; i < 500; i++ {
		v := randBytes(rng, rng.Intn(300))
		want := crc32.ChecksumIEEE(v)
		if got := crcseg.Checksum(v); got != want {
			return mismatch(fmt.Sprintf("Checksum(%d 字节)=%#08x", len(v), want),
				fmt.Sprintf("%#08x", got))
		}
	}
	return nil
}

// UPD2：分段推进必须可结合 —— 把同一次切分再切一刀，结果不变。
func chunkedUpdateComposition() error {
	rng := rand.New(rand.NewSource(102))

	for i := 0; i < 400; i++ {
		seed := rng.Uint32() | 1
		a := randBytes(rng, 1+rng.Intn(64))
		b := randBytes(rng, 1+rng.Intn(64))

		chained := crcseg.Update(crcseg.Update(seed, a), b)
		joined := append(append([]byte(nil), a...), b...)
		once := crcseg.Update(seed, joined)

		if chained != once {
			return mismatch(fmt.Sprintf("Update(Update(%#08x, a), b)=%#08x", seed, once),
				fmt.Sprintf("%#08x", chained))
		}
	}
	return nil
}

// UPD3：任意随机分段下，增量推进的结果必须等于对整段调用 Checksum 的结果。
func incrementalEqualsFull() error {
	rng := rand.New(rand.NewSource(103))

	for _, total := range []int{0, 1, 2, 3, 5, 17, 64, 1000, 65536} {
		data := randBytes(rng, total)

		crc := uint32(0)
		for i := 0; i < len(data); {
			n := 1 + rng.Intn(97)
			if i+n > len(data) {
				n = len(data) - i
			}
			crc = crcseg.Update(crc, data[i:i+n])
			i += n
		}

		want := crc32.ChecksumIEEE(data)
		if crc != want {
			return mismatch(fmt.Sprintf("整段 %d 字节 Checksum=%#08x", len(data), want),
				fmt.Sprintf("分段推进=%#08x", crc))
		}
	}
	return nil
}

// ------------------------------------------------------------------ combine

// CMB1：两段合成。期望值由整段数据独立算出，被测实现只拿到两段校验值与 lenB。
func combineTwoSegments() error {
	rng := rand.New(rand.NewSource(201))

	for i := 0; i < 300; i++ {
		a := randBytes(rng, 1+rng.Intn(300))
		b := randBytes(rng, 1+rng.Intn(300))

		want := crc32.ChecksumIEEE(append(append([]byte(nil), a...), b...))
		got := crcseg.Combine(crcseg.Checksum(a), crcseg.Checksum(b), uint64(len(b)))

		if got != want {
			return mismatch(fmt.Sprintf("Checksum(A||B)=%#08x（|A|=%d, |B|=%d）", want, len(a), len(b)),
				fmt.Sprintf("Combine=%#08x", got))
		}
	}

	// 第一段为空：此时 Combine(0, Checksum(B), len(B)) 必须等于 Checksum(B)。
	for i := 0; i < 50; i++ {
		b := randBytes(rng, 1+rng.Intn(300))
		want := crc32.ChecksumIEEE(b)
		got := crcseg.Combine(0, crcseg.Checksum(b), uint64(len(b)))
		if got != want {
			return mismatch(fmt.Sprintf("首段为空时 Checksum(B)=%#08x", want), fmt.Sprintf("Combine=%#08x", got))
		}
	}
	return nil
}

// CMB2：三段合成，左折两次。
func combineThreeSegments() error {
	rng := rand.New(rand.NewSource(202))

	for i := 0; i < 300; i++ {
		a := randBytes(rng, 1+rng.Intn(200))
		b := randBytes(rng, 1+rng.Intn(200))
		c := randBytes(rng, 1+rng.Intn(200))

		all := append(append(append([]byte(nil), a...), b...), c...)
		want := crc32.ChecksumIEEE(all)

		ab := crcseg.Combine(crcseg.Checksum(a), crcseg.Checksum(b), uint64(len(b)))
		got := crcseg.Combine(ab, crcseg.Checksum(c), uint64(len(c)))

		if got != want {
			return mismatch(fmt.Sprintf("Checksum(A||B||C)=%#08x", want), fmt.Sprintf("左折两次=%#08x", got))
		}
	}
	return nil
}

// CMB3：多段（含长度 0 的段）逐段左折，与整段结果对拍。
func combineManySegments() error {
	rng := rand.New(rand.NewSource(203))

	// 覆盖若干规模，最大 64 段。
	for _, count := range []int{2, 3, 5, 8, 17, 64} {
		for round := 0; round < 40; round++ {
			var all []byte
			crc := uint32(0)

			for k := 0; k < count; k++ {
				var seg []byte
				if rng.Intn(6) == 0 {
					seg = []byte{}
				} else {
					seg = randBytes(rng, 1+rng.Intn(128))
				}
				all = append(all, seg...)
				crc = crcseg.Combine(crc, crcseg.Checksum(seg), uint64(len(seg)))
			}

			want := crc32.ChecksumIEEE(all)
			if crc != want {
				return mismatch(fmt.Sprintf("%d 段的整段 Checksum=%#08x（共 %d 字节）", count, want, len(all)),
					fmt.Sprintf("逐段合成=%#08x", crc))
			}
		}
	}
	return nil
}

// --------------------------------------------------------------------- edge

// EDG1：空片段是恒等元。
func emptySegmentIdentity() error {
	rng := rand.New(rand.NewSource(301))

	for i := 0; i < 64; i++ {
		crc := rng.Uint32()
		if crc == 0 {
			crc = 0xFFFFFFFF
		}

		if got := crcseg.Update(crc, nil); got != crc {
			return mismatch(fmt.Sprintf("Update(%#08x, nil)=%#08x", crc, crc), fmt.Sprintf("%#08x", got))
		}
		if got := crcseg.Update(crc, []byte{}); got != crc {
			return mismatch(fmt.Sprintf("Update(%#08x, 空切片)=%#08x", crc, crc), fmt.Sprintf("%#08x", got))
		}
		if got := crcseg.Combine(crc, 0, 0); got != crc {
			return mismatch(fmt.Sprintf("Combine(%#08x, 0, 0)=%#08x", crc, crc), fmt.Sprintf("%#08x", got))
		}
	}

	// 空拼接的校验值就是 0。
	if got := crcseg.Update(0, nil); got != 0 {
		return mismatch("Update(0, nil)=0x00000000", fmt.Sprintf("%#08x", got))
	}
	return nil
}

// EDG2：lenB 是 uint64，超过 2^32 的长度必须按多项式模运算处理，不许截断。
func lenbUint64Range() error {
	// (1) 可物化的一段：1 MiB，期望值用整段 crc32 独立算出。
	a := patterned(0x5A, 12345)
	b := patterned(0xA5, 1<<20)

	want := crc32.ChecksumIEEE(append(append([]byte(nil), a...), b...))
	got := crcseg.Combine(crcseg.Checksum(a), crcseg.Checksum(b), uint64(len(b)))
	if got != want {
		return mismatch(fmt.Sprintf("1 MiB 第二段的整段 Checksum=%#08x", want), fmt.Sprintf("Combine=%#08x", got))
	}

	// (2) 超过 2^32 的 lenB：期望值已离线核对（在可物化的长度上与整段 crc32 一致，
	//     并满足 x^(8·lenB) 的周期性质）。cA/cB 由两个固定片段算出。
	cA := crc32.ChecksumIEEE([]byte("segment-A/payload"))
	cB := crc32.ChecksumIEEE([]byte("B!"))

	vectors := []struct {
		lenB uint64
		want uint32
	}{
		{1, 0xC46B292B},
		{1 << 31, 0x76BF4FF6},
		{1<<32 - 1, 0x802603E9},
		{1 << 32, 0xC46B292B},
		{(1 << 32) + 9, 0x899F631A},
		{1 << 33, 0xB145C79D},
		{(1 << 40) + 7, 0xBBD7D8C0},
	}

	for _, v := range vectors {
		got := crcseg.Combine(cA, cB, v.lenB)
		if got != v.want {
			return mismatch(fmt.Sprintf("lenB=%d 时 Combine=%#08x", v.lenB, v.want),
				fmt.Sprintf("%#08x", got))
		}
	}
	return nil
}

// --------------------------------------------------------------------- wire

// WIR1：Marshal 必须是 4 字节小端。
func marshalLittleEndian() error {
	values := []uint32{0, 1, 0x000000FF, 0x01020304, 0xCAFEBABE, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFF}

	for _, v := range values {
		want := []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
		got := crcseg.Marshal(v)

		if len(got) != 4 {
			return mismatch(fmt.Sprintf("Marshal(%#08x) 是 4 字节", v), fmt.Sprintf("%d 字节", len(got)))
		}
		if !bytes.Equal(got, want) {
			return mismatch(fmt.Sprintf("Marshal(%#08x)=% x", v, want), fmt.Sprintf("% x", got))
		}

		back, err := crcseg.Unmarshal(got)
		if err != nil {
			return mismatch(fmt.Sprintf("Unmarshal(Marshal(%#08x)) 无错", v), err.Error())
		}
		if back != v {
			return mismatch(fmt.Sprintf("Unmarshal(Marshal(%#08x))=%#08x", v, v), fmt.Sprintf("%#08x", back))
		}
	}

	// 小端字节串也要能被 Unmarshal 读回。
	wire := []byte{0x04, 0x03, 0x02, 0x01}
	got, err := crcseg.Unmarshal(wire)
	if err != nil {
		return mismatch("Unmarshal(04 03 02 01) 无错", err.Error())
	}
	if got != 0x01020304 {
		return mismatch("Unmarshal(04 03 02 01)=0x01020304", fmt.Sprintf("%#08x", got))
	}
	return nil
}

// WIR2：编解码往返成对，长度不对必须报错而不是 panic。
func roundtripAndErrors() error {
	rng := rand.New(rand.NewSource(401))

	for i := 0; i < 500; i++ {
		crc := rng.Uint32()

		wire := crcseg.Marshal(crc)
		if len(wire) != 4 {
			return mismatch(fmt.Sprintf("Marshal(%#08x) 是 4 字节", crc), fmt.Sprintf("%d 字节", len(wire)))
		}

		got, err := crcseg.Unmarshal(wire)
		if err != nil {
			return mismatch(fmt.Sprintf("Unmarshal(Marshal(%#08x)) 无错", crc), err.Error())
		}
		if got != crc {
			return mismatch(fmt.Sprintf("往返后 %#08x", crc), fmt.Sprintf("%#08x", got))
		}
	}

	for _, n := range []int{0, 1, 2, 3, 5, 8, 16} {
		if _, err := crcseg.Unmarshal(make([]byte, n)); err == nil {
			return mismatch(fmt.Sprintf("长度 %d 的输入返回错误", n), "nil")
		}
	}
	return nil
}
