# Badger 存储格式：Value Log（.vlog）与 SSTable（.sst）编码

本文专注“字节级布局”：

- valuePointer 在 LSM 中的表示
- vlog（.vlog）单条 entry 的编码
- sstable（.sst）文件整体布局、block 布局、index 与 checksum
- 压缩/加密对布局的影响

---

## 1. LSM 中的 value 表示：ValueStruct +（value 或 valuePointer）

Badger 内部用 `y.ValueStruct`（`y/iterator.go:15`）承载 LSM 的 value：

- `Meta` (1 byte)
- `UserMeta` (1 byte)
- `ExpiresAt` (uvarint)
- `Value` (remaining bytes)

编码：`y/iterator.go:52`。

其中 `Value` 的语义由 `Meta` 控制：

- 若 `Meta & bitValuePointer == 0`：`Value` 就是用户 value（内联在 LSM）
- 若 `Meta & bitValuePointer != 0`：`Value` 是 `valuePointer.Encode()` 的定长字节

### 1.1 valuePointer 的编码

`valuePointer`（`structs.go:15`）：

- `Fid uint32`
- `Len uint32`
- `Offset uint32`

编码：`structs.go:38`

- 直接用 `unsafe` 把 struct 的内存布局写入 byte slice（并用 copy/对齐规避问题）。

备注：这意味着 valuePointer 编码受 endianness/平台影响的风险由实现方控制；Badger 通过约束与对齐处理在主要平台上工作。

---

## 2. Value Log（.vlog）编码

vlog entry 的编码函数：`logFile.encodeEntry`（`memtable.go:283`）。

### 2.1 entry 布局

```
+--------+-----+-------+-------+
| header | key | value | crc32 |
+--------+-----+-------+-------+
```

- header：变长
- key/value：原始字节（若启用加密则为密文）
- crc32：固定 4 字节（CRC32C Castagnoli），覆盖 header+key+value

### 2.2 header 编码

header（`structs.go:56`）：

- meta (byte)
- userMeta (byte)
- klen (uvarint)
- vlen (uvarint)
- expiresAt (uvarint)

编码：`structs.go:75`；解码：`structs.go:92`。

### 2.3 vlog 加密

若启用加密：

- header 明文
- 仅 key+value 通过 XOR stream 加密
- IV 由 `offset` 派生（`generateIV(offset)`）

见 `memtable.go:304`~`memtable.go:317`。

---

## 3. SSTable（.sst）编码

SSTable 由 `table/Builder` 构建（`table/builder.go`），由 `table/Table` 读取（`table/table.go`）。

### 3.1 文件整体布局

`table/builder.go:381`：

```
| Block 1 | Block 2 | ... | Block N | Index | IndexSize(u32) | Checksum | ChecksumSize(u32) |
```

读取时从文件尾部反向解析（`table/table.go:initIndex`）。

- `ChecksumSize`：最后 4 字节
- `Checksum`：protobuf `pb.Checksum` 的序列化
- `IndexSize`：再往前 4 字节
- `Index`：FlatBuffers `fb.TableIndex`

### 3.2 Block（数据块）布局（读取侧视角）

读取 block 的逻辑在 `Table.block`（`table/table.go:533`）。从 block 尾部解析：

1) `chkLen`：最后 4 字节（u32）
2) `checksum`：向前 `chkLen`
3) `numEntries`：再向前 4 字节（u32）
4) `entryOffsets`：`numEntries * 4` 字节（u32 数组）

最终：

- `blk.entryOffsets` 指向 offsets 数组（用于二分定位 entry）
- `blk.data` 被裁剪为“实际数据区 + entryOffsets + numEntries”

### 3.3 Block 内 entry 的 key 压缩（前缀压缩）

Builder 使用一个 `header{overlap,diff}`（`table/builder.go:37`）实现 prefix compression：

- `baseKey`：当前 block 的基准 key（通常是 block 首 key）
- 对后续 key：
  - overlap = 与 baseKey 的公共前缀长度
  - diff = key 剩余部分长度

这样能显著减少 key 存储开销。

### 3.4 Index：TableIndex（FlatBuffers）

Index 由 `Builder.buildIndex`（`table/builder.go:526`）生成，包含：

- `Offsets[]`：每个 block 的 `{key, offset, len}`
- `BloomFilter`：可选
- 统计字段：`MaxVersion/KeyCount/UncompressedSize/OnDiskSize/StaleDataSize` 等

读取：`Table.readTableIndex`（`table/table.go:684`）+ `fetchIndex`（缓存/加密场景）。

### 3.5 压缩/加密

- 压缩通常是 block 级别（builder 后台 goroutine 做压缩/加密）
- 若开启加密：
  - block 读取时先 decrypt 再 decompress（`Table.block`）
  - index 也可加密：`Builder.encrypt` 会在 index 末尾追加 IV（`table/builder.go:488`）

