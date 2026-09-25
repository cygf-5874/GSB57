# crcseg —— 分段校验和合成

CRC-32/IEEE 的分段校验库（Go 1.24，仅标准库，`go.mod` 无 `require`）。

面向分段传输的对账：发送端逐段推进校验值，接收端只拿到每一段的校验值与长度，
就要能算出拼接之后的校验值 —— 双方都不需要保留原始数据。

```go
// 发送端：逐段推进
crc := crcseg.Checksum(seg0)
crc = crcseg.Update(crc, seg1)
crc = crcseg.Update(crc, seg2)

// 接收端：只拿两段的校验值与第二段长度
joined := crcseg.Combine(crcA, crcB, uint64(lenB))

// 线上传输
wire := crcseg.Marshal(crc)          // 4 字节
back, err := crcseg.Unmarshal(wire)
```

## 怎么跑

```bash
go test ./...            # 既有用例
go run ./segcheck        # 验收场景（固定件，10 个）
go run ./segcheck -list
go run ./segcheck -only combine   # --only combine 等价
go run repro.go          # 上游同步链路抓下来的复现脚本
```

## 常量

- `Poly = 0xEDB88320`：CRC-32/IEEE 的多项式（反射表示）。

整套参数写死为 CRC-32/ISO-HDLC：多项式 `0x04C11DB7`（反射 `0xEDB88320`）、
初值 `0xFFFFFFFF`、输入输出反射、终值掩码 `0xFFFFFFFF`。不许换多项式。

## 对外保证

下面 7 条同时成立，一条都不能少。**验收以 `segcheck/` 为准**，语义细节以本节为准。

### 1. `Checksum` 与标准库逐位一致

`Checksum(data []byte) uint32` 必须与 `hash/crc32.ChecksumIEEE(data)` **逐位相同**，
对任意长度（含 0）的输入都成立。

### 2. `Update` 是增量接口

`Update(crc uint32, chunk []byte) uint32` 把 `chunk` 的字节并进已有的校验值：

```
Update(Update(0, a), b) == Checksum(append(a, b...))
```

更一般地，把一段数据切成任意多片、按顺序 `Update` 起来（起点用 `Checksum(第一片)`
或 `Update(0, 第一片)`），结果必须等于对整段调一次 `Checksum`。切分方式不影响结果。

### 3. `Combine` 只靠两段的校验值与长度

`Combine(crcA, crcB uint32, lenB uint64) uint32`：

- `crcA` 是前一段的校验值（前一段多长都行），`crcB` 是后一段的校验值，
  `lenB` 是**后一段的字节长度**；
- 返回值必须等于 `Checksum(前一段 || 后一段)`；
- 实现**不许依赖前一段的原始数据**，也不许要求调用方把两段拼起来 ——
  调用方只提供两个校验值和一个长度。

`Combine` 必须与 `Update` 一致：`Combine(crcA, Checksum(B), len(B))` 等于
把 `B` 用 `Update` 并进 `crcA` 的结果。

### 4. 空片段是恒等元

- `Update(crc, nil) == crc`，`Update(crc, []byte{}) == crc`；
- `Combine(crc, 0, 0) == crc`；
- 空数据的校验值是 `0`：`Checksum(nil) == 0`。

### 5. `lenB` 是 `uint64`

`lenB` 的取值范围是整个 `uint64`。**超过 `2^32` 的长度必须按标准的多项式模运算处理**，
不许因为内部用了 32 位（或 `int`）而把它截断、取模或直接忽略。

### 6. 线上字节序是小端

`Marshal(crc uint32) []byte` 产出**恰好 4 个字节、小端**（低位字节在前）：

```
Marshal(0x01020304) == []byte{0x04, 0x03, 0x02, 0x01}
```

`Unmarshal(b []byte) (uint32, error)` 与它互逆；`len(b) != 4` 时返回 `ErrBadLength`，
不许 panic。

### 7. 不保留输入数据

`Update` / `Combine` 的调用方只提供片段与长度，实现不得缓存整段数据留待重算，
也不得改写调用方传入的切片。

## 验收

`segcheck/` 是固定验收程序，**不要修改**。10 个场景分四组：

| 组 | 场景 | 覆盖 |
| --- | --- | --- |
| `update` | `UPD1_checksum_matches_stdlib` | 与 `hash/crc32` 对拍（保证 1） |
| `update` | `UPD2_chunked_update_composition` | 分段推进的可结合性（保证 2） |
| `update` | `UPD3_incremental_equals_full` | 任意随机分段下增量结果 = 整段结果（保证 2） |
| `combine` | `CMB1_two_segments` | 两段合成 = 整段校验值（保证 3） |
| `combine` | `CMB2_three_segments` | 三段左折合成（保证 3） |
| `combine` | `CMB3_many_segments` | 2~64 段（含空段）逐段合成（保证 3、4） |
| `edge` | `EDG1_empty_segment_identity` | 空片段恒等元（保证 4） |
| `edge` | `EDG2_lenb_uint64_range` | `lenB` 覆盖 `2^31`~`2^40` 等长度（保证 5） |
| `wire` | `WIR1_marshal_little_endian` | 小端 4 字节与 `Unmarshal` 互逆（保证 6） |
| `wire` | `WIR2_roundtrip_and_errors` | 往返、长度校验（保证 6） |

- `go run ./segcheck` 退出码 0，即 10 个场景全过；
- 判据只调用被测库的接口：随机分段与整段结果对拍时，期望值由固定件用 `hash/crc32`
  对整段数据独立算出，**被测实现只拿到片段与长度**；
- 每个场景在独立子进程里执行，挂死不会遮蔽其余场景；子进程有 25 秒看门狗，
  超时会转储全部 goroutine 栈并以退出码 3 结束。失败不早退。

## 目录

```
.
├── go.mod            module crcseg
├── errors.go         ErrBadLength
├── crcseg.go         Poly / Checksum / Update / Combine / Marshal / Unmarshal
├── crcseg_test.go    既有用例
├── repro.go          上游同步链路抓下来的复现脚本（go run repro.go）
├── segcheck/main.go  固定验收程序（勿改）
└── README.md
```

## 版本前提

- Go 1.24 及以上，仅标准库，不需要联网、数据库或中间件。
- 判据完全确定：固定种子的 PRNG，不依赖时间、随机源或机器速度。
