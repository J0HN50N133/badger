# Badger Compaction 详解（含多版本回收与 vlog GC 统计）

本文专注讲解 Badger 的 LSM compaction：

- compaction 的调度与优先级
- 一次 compaction 的输入/输出与并行子任务（subcompaction）
- 多版本清理（discardTs / NumVersionsToKeep / tombstone 保留策略）
- compaction 如何为 value log GC 累积 DISCARD 统计

核心代码：`levels.go`, `compaction.go`, `level_handler.go`。

---

## 1. Compaction 的目标

Badger 的 LSM tree 由多层 level（L0..Lmax）组成：

- L0：由 memtable flush 产生，可能互相重叠
- L1+：范围分片更稳定（更少重叠）

Compaction 的目标：

- 降低读放大：把重叠/分散的数据合并成更少、更有序的表
- 清理过期/删除/旧版本：在不破坏事务快照的前提下释放空间
- 为 vlog GC 提供“可丢弃 value”统计（DISCARD）

---

## 2. 调度：startCompact 与 pickCompactLevels

DB Open 时启动 compactor 线程：

- `db.lc.startCompact(db.closers.compactors)`（`db.go:345`）

`levelsController.startCompact`（`levels.go:344`）会启动多个 compactor goroutine（数量由配置决定），循环：

1) `pickCompactLevels` 计算每层的 compaction priority（`levels.go:540`）
2) 根据 score/adjusted score 选择目标层，调用 `doCompact`

优先级模型参考 RocksDB leveled compaction（`levels.go:537` 注释）。

---

## 3. doCompact 的高层流程

`doCompact(id, p)`（`levels.go:1507`）大体做：

- 选定 compaction 定义 `compactDef`：
  - `thisLevel` 的表集合
  - `nextLevel` 的重叠表集合
  - compaction key range
- 构造迭代器 merge 所有输入表
- 调用 `subcompact` 产出新表
- 更新 manifest / level handler
- 释放旧表引用（最终删除）

并发安全通过 `cstatus`（compactionStatus）追踪区间与层级，避免冲突 compaction。

---

## 4. subcompact：按 key range 并行生成新表

`subcompact`（`levels.go:638`）是 compaction 的核心写出逻辑。

当底层表较多时，会拆成多个 keyRange 并行跑（`levels.go:631` 注释）：

- 对每个 keyRange：
  - 使用 iterator 只遍历该范围
  - 把输出写入一个或多个新的 SSTable（table.Builder）

建表本身会开启 goroutine 并受 `inflightBuilders` throttle 限制（`levels.go:833` 附近），避免同时构建太多表导致内存/CPU 峰值。

---

## 5. 多版本清理（MVCC 安全边界）

### 5.1 discardTs：只能清理到哪里

Compaction 会取：

- `discardTs := s.kv.orc.discardAtOrBelow()`（`levels.go:648`）

含义：

- 非 managed 模式：最老的活跃读事务的 readTs（watermark）
- managed 模式：用户设置的 discardTs

安全性：

- 仅当 `version <= discardTs` 时，才允许物理删除旧版本，否则会破坏快照读。

### 5.2 NumVersionsToKeep 与 bitDiscardEarlierVersions

对同一 user key 的多个版本，compaction 迭代时：

- 若 `version <= discardTs` 且非 merge entry（`levels.go:754`）
  - `numVersions++`
  - 当满足以下之一时认为到达“最后一个需要保留的版本”：
    - `vs.Meta&bitDiscardEarlierVersions > 0`
    - `numVersions == Options.NumVersionsToKeep`

之后的更老版本（同 key）会被跳过（`skipKey`），达到清理效果。

### 5.3 tombstone（删除标记）是否要保留

关键条件：是否与更低层（nextLevel+1...）有 overlap。

- `hasOverlap := checkOverlap(..., cd.nextLevel.level+1)`（`levels.go:642`）

若某个 key 在该 compaction 的输出范围内 **不会与更低层重叠**，那么 tombstone 和更老版本可以更激进地丢弃；否则需要保留 tombstone 以覆盖下层的旧值。

对应逻辑：`levels.go:775`~`levels.go:795`。

---

## 6. Compaction 与 Value Log GC 的关系（DISCARD 统计）

Badger 的 vlog GC 需要知道“哪些 vlog 文件里垃圾最多”。垃圾量主要来自：

- LSM compaction 丢弃旧版本/删除版本时，这些版本如果存的是 vptr（指向 vlog），对应的 vlog entry 就变成垃圾。

在 `subcompact` 里，凡是决定“跳过/丢弃”的记录，会调用 `updateStats(vs)`：

- 如果 `vs.Meta&bitValuePointer > 0`：
  - `vp.Decode(vs.Value)`
  - `discardStats[vp.Fid] += int64(vp.Len)`（`levels.go:656`~`levels.go:667`）

这些统计最终汇总提交给 `valueLog.updateDiscardStats`，写入 `DISCARD` mmap 文件。

因此：

- **没有 compaction 的情况下，DISCARD 增长慢，vlog GC 也就难以挑到“值得 GC”的文件。**

---

## 7. 读写放大与性能权衡

- compaction 过慢：L0 堆积，读放大上升，写入可能被迫等待
- compaction 过激进：CPU/I/O 压力大
- vlog GC 会触发一波 LSM 更新（大量 vptr 重写），间接增加 compaction 压力

建议把 compaction 与 GC 看作一个耦合系统来调参：

- 先保证 compaction 跑得动（避免 L0 积压）
- 再用合理的 `discardRatio` 调用 GC

