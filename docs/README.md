# Badger 文档索引（补充）

除了 `docs/index.md` 中的对外文档，这里补充一些偏“实现细节/源码导读”的内容：

- `internal-mechanics.md`：总览（GC/Compaction/编码/调度/MVCC）
- `gc.md`：Value Log GC 详解
- `compaction.md`：Compaction 详解（含版本回收与 vlog GC 统计）
- `formats.md`：vlog 与 sstable 的编码格式
- `scheduling.md`：前后台 goroutine 调度与写入管线
- `mvcc.md`：多版本（MVCC/SSI）与 discardTs

