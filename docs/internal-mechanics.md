# Badger 内部机制：GC、Compaction、编码、调度与多版本

这是一份“导航型”文档：给出 Badger 的关键内部流程与源码入口，并把细节拆分到独立章节。

- GC（Value Log GC）：`gc.md`
- Compaction：`compaction.md`
- 存储格式（vlog/sstable）：`formats.md`
- 前后台调度：`scheduling.md`
- 多版本与版本回收（discardTs）：`mvcc.md`

如果你只想快速建立整体心智模型，建议先读 `scheduling.md` 再回到本文。

---

## 1. 系统分层与核心思想（WiscKey）

Badger 将数据分成两条存储路径：

- **LSM Tree（memtable + SSTable）**：存 key、元信息、以及“小 value 或 vptr”。
- **Value Log（.vlog）**：存“大 value 的真实字节”。

关键关联：LSM 中通过 `valuePointer{Fid,Offset,Len}` 指向 vlog entry。

- `valuePointer` 定义：`structs.go:15`
- value 在 LSM 的编码：`y/iterator.go:15`

详见：`formats.md`。

---

## 2. 写入路径（高层）

- 写入请求进入 `db.writeCh`
- `db.doWrites` 单 goroutine 串行处理：
  - 大 value → 写 vlog → 得 vptr
  - 写 memtable（含 WAL）
- memtable 满时转 immutable，并投递到 `flushChan`
- `flushMemtable` 把 immutable memtable 刷成 L0 table
- 后台 compaction 持续合并各层

详见：`scheduling.md`。

---

## 3. Compaction（LSM 合并 + 版本回收）

核心入口：

- 选优先级：`levels.go:pickCompactLevels`
- 执行：`levels.go:doCompact`
- 关键写出：`levels.go:subcompact`

核心机制：

- subcompaction：按 key range 拆分并行建表
- 多版本回收：由 `discardTs` 与 `NumVersionsToKeep`/`bitDiscardEarlierVersions` 控制
- tombstone 在是否 overlap 场景下的保留策略
- 为 vlog GC 累积 DISCARD：丢弃 vptr 时累加 `vp.Len` 到 `vp.Fid`

详见：`compaction.md` 与 `mvcc.md`。

---

## 4. Value Log GC（vlog rewrite）

对外入口：

- `DB.RunValueLogGC(discardRatio)`（`db.go:1229`）

核心：

- 通过 `DISCARD` 统计选择 vlog 文件（`value.go:pickLog`, `discard.go`）
- 单 GC 互斥：`valueLog.garbageCh`
- rewrite：扫描旧 vlog，仍有效 entry 搬迁到新 vlog，并更新 LSM vptr

详见：`gc.md`。

---

## 5. 存储编码（vlog / sstable）

- vlog entry：`header + key + value + crc32`（`memtable.go:encodeEntry`, `structs.go:header`）
- sstable：`blocks + index + indexLen + checksum + checksumLen`（`table/builder.go`, `table/table.go`）
- 压缩/加密：block/index 的压缩与可选加密

详见：`formats.md`。

---

## 6. 前后台 goroutine 调度

Badger 的并发模型可概括为：

- 前台写入串行化（`doWrites`）
- 后台 flush（`flushMemtable`）
- 后台 compaction（多 compactor + 子任务并行建表）
- GC 由用户触发且互斥

详见：`scheduling.md`。

---

## 7. 多版本（MVCC/SSI）与 discardTs

- oracle 分配 readTs/commitTs
- `readMark/txnMark` watermark 保证快照一致
- `discardTs` 决定 compaction 能删除到哪里

详见：`mvcc.md`。

