# Badger GC（Value Log GC）详解

本文专注讲解 Badger 的 **Value Log（vlog）GC**：它回收 `.vlog` 文件中的无效 value，降低磁盘占用。

相关核心代码：

- 对外入口：`db.go:RunValueLogGC`
- 选择/执行 GC：`value.go:runGC/pickLog/doRunGC/rewrite`
- 可丢弃统计：`discard.go`（`DISCARD` mmap 文件）
- compaction 侧统计上报：`levels.go:subcompact` → `valueLog.updateDiscardStats`

---

## 1. 为什么需要 vlog GC

Badger 采用 key/value 分离：

- LSM（memtable + sst）保存 key 与元信息；大 value 写入 vlog。
- 随着更新与删除，vlog 中旧 value 会变“垃圾”（LSM 已经指向新位置或 tombstone）。

LSM compaction 只能清理 LSM 内部的旧版本/删除标记；**vlog 的空间回收需要专门的 GC**。

---

## 2. 触发方式与并发限制

### 2.1 触发

Badger 不会默认自动周期性 GC；通常由上层应用定期调用：

- `DB.RunValueLogGC(discardRatio)`（`db.go:1229`）

参数 `discardRatio` 表示“该 vlog 文件内可回收字节占比达到多少才值得重写”。

### 2.2 同时只能跑一个 GC

`valueLog.garbageCh` 是容量为 1 的 channel（`value.go:543`），用作互斥锁：

- `runGC`（`value.go:1076`）尝试 `vlog.garbageCh <- struct{}{}`
- 如果 channel 已满，说明已有 GC 在运行，返回 `ErrRejected`

### 2.3 关闭时如何收敛 GC

DB Open 时会启动一个 gate goroutine：

- `go db.vlog.waitOnGC(db.closers.valueGC)`（`db.go:383`）

`waitOnGC`（`value.go:1066`）在关闭时：

- 等待 closer 关闭信号
- 往 `garbageCh` 里塞一个 token 并不取出，阻止后续 GC
- 并等待正在进行的 GC 结束

---

## 3. 选择要 GC 的 vlog 文件：DISCARD 统计

### 3.1 DISCARD 文件

`discardStats`（`discard.go`）是一个 mmap 文件（默认 `ValueDir/DISCARD`）：

- 每条记录 16 bytes：
  - 8 bytes fid
  - 8 bytes discardBytes

`Update(fid, discard)` 会：

- `discard > 0`：累加该 fid 的可回收字节
- `discard == 0`：读取当前值
- `discard < 0`：把该 fid 的值清零（或删除文件时置零）

### 3.2 discardBytes 从哪里来

核心来源是 **LSM compaction**：当 compaction 丢弃某条记录时，如果它的 value 存在 vlog（即 `bitValuePointer`），就把该 vptr 指向的 vlog entry 长度 `vp.Len` 计入 discardBytes。

见：`levels.go:subcompact` 内的 `updateStats`（`levels.go:656` 起）。

Compaction 结束后会调用 `vlog.updateDiscardStats(stats)`（`value.go:1096` 一带），更新 DISCARD。

### 3.3 pickLog 选择策略

`pickLog(discardRatio)`（`value.go:998` 一带）策略：

1) `fid, discard := discardStats.MaxDiscard()`
2) 若 `discard < discardRatio * fileSize`，认为不值得 GC
3) 若 `fid == maxFid`（当前可写 vlog 文件），不做 GC（避免与写入冲突）

满足条件则返回该 `logFile` 给 rewrite。

---

## 4. GC 的核心：rewrite（重写 + 更新 LSM 指针）

`doRunGC(lf)`（`value.go:1054`）调用 `vlog.rewrite(lf)`。

rewrite 的目标：

- 扫描旧文件 lf 中的所有 entry
- 对每条 entry 判断是否仍然“有效/可达”
- 仍有效的：写入新的 vlog 位置，并更新 LSM 中对应 key 的 vptr
- 最后删除旧文件（在安全条件满足时）

### 4.1 如何判断一条 vlog entry 是否应丢弃

辅助函数：`discardEntry(e, vs, db)`（`value.go:1035`）核心判定：

- LSM 中该 key 的最新状态与 vlog entry 的 version 不一致：丢弃
  - `vs.Version != ParseTs(e.Key)`
- LSM 状态显示 deleted/expired：丢弃
- LSM 已经内联 value（不是 vptr）：丢弃（说明 value 不再依赖 vlog）
- `bitFinTxn` 这种纯事务结束标记：丢弃

注意：vlog entry 的 key 是 internal key（包含 ts），因此 GC 会严格比对版本。

### 4.2 为什么 vlog 里不写事务标记

`valueLog.write`（`value.go:855`~`value.go:874`）在写 vlog 前会清掉 `bitTxn|bitFinTxn`：

- vlog entry 必须可被顺序扫描并正确解析。
- 若 vlog 里混入事务边界标记，GC 很难保证扫描一致性。
- 事务标记只需要保留在 memtable WAL（用于崩溃恢复/重放）。

### 4.3 删除旧 vlog 文件的时机

rewrite 完成后，会尝试把旧 lf 从 `filesMap` 删除并删除文件（`value.go:340` 一带）：

- 如果当前没有活跃 iterator（`iteratorCount()==0`），可立即删除
- 否则把 fid 放入 `filesToBeDeleted`，等最后一个 iterator 关闭时批量删除（`value.go:356`~`value.go:390`）

这保证了正在进行的扫描/迭代不会读到被删除的 mmap。

---

## 5. 你在业务侧如何安全使用 RunValueLogGC

典型做法：

- 周期性触发，例如每分钟/每小时调用一次
- 从较高 `discardRatio` 开始（例如 0.5），逐步降低（0.3/0.2），避免过度重写导致 I/O 抖动
- 观察写放大与 compaction 压力：GC 会带来一波 LSM 更新（大量 vptr 更新写入）

---

## 6. 常见现象与排查

- `ErrNoRewrite`：说明当前最“脏”的 vlog 文件的 discardBytes 仍低于阈值；正常。
- `ErrRejected`：已有 GC 在运行；重试或串行调度。
- vlog 迟迟不下降：
  - 可能 compaction 压力大，discardStats 还没积累
  - 或活跃长事务导致 `discardTs` 很小（旧版本不能丢弃），进而无法累积 discard

