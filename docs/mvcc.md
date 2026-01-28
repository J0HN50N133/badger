# Badger 多版本（MVCC/SSI）与版本回收（discardTs）

本文专注 Badger 的多版本管理：

- internal key（user key + ts）的基本思想
- oracle 如何分配 readTs/commitTs
- watermark（readMark/txnMark）如何保障快照读
- discardTs 如何决定 compaction 可以删除哪些版本

相关代码：`txn.go`, `levels.go`。

---

## 1. internal key：用 ts 表示版本

Badger 以“版本号 ts（uint64）”实现 MVCC：

- 每次事务提交分配一个单调递增的 `commitTs`
- key 在 LSM 中按 internal key 排序（userKey + ts）
- 同一 userKey 的多个版本在迭代时会按版本顺序出现（通常从新到旧）

这样：

- 读事务只要固定一个 `readTs`，就能在遍历时选到“<= readTs 的最新版本”。

---

## 2. oracle：readTs/commitTs 分配与一致性

oracle 定义在 `txn.go:24`。

### 2.1 readTs 的获取

`oracle.readTs()`（`txn.go:78`）：

- `readTs = nextTxnTs - 1`
- 调用 `readMark.Begin(readTs)` 标记一个活跃读
- 等待 `txnMark.WaitForMark(readTs)`

等待的意义：

- 确保所有 <= readTs 的提交都已经完成“写入 LSM/vlog 的可见化”，否则可能读不到刚提交的数据。

### 2.2 commitTs 的获取

`oracle.newCommitTs(txn)`（`txn.go:153`）在非 managed 模式：

- 先做冲突检测（可选）
- `ts = nextTxnTs; nextTxnTs++`
- `txnMark.Begin(ts)`

提交完成后：

- `oracle.doneCommit(cts)`（`txn.go:230`）会 `txnMark.Done(cts)`

这对 watermark 的组合保证：

- 新读事务拿到的 readTs 不会“越过”还未完全写入完成的提交。

---

## 3. readMark/txnMark：两个 watermark 的职责

- `readMark`（`txn.go:40`）：追踪活跃读事务的 readTs
  - `Begin(readTs)` 在事务开始
  - `Done(readTs)` 在事务结束
  - `DoneUntil()` 返回“当前所有活跃读中最小 readTs”（更准确：watermark 能推进到的最小边界）

- `txnMark`（`txn.go:35`）：追踪已分配 commitTs 但尚未完全完成的提交
  - `Begin(commitTs)` 在分配 commitTs 时
  - `Done(commitTs)` 在提交写入完成后
  - `WaitForMark(readTs)` 用于确保 <= readTs 的提交都已 Done

---

## 4. discardTs：决定 compaction 的清理边界

compaction 时会取：

- `discardTs := orc.discardAtOrBelow()`（`levels.go:648`）

定义：`oracle.discardAtOrBelow()`（`txn.go:118`）：

- managed 模式：返回用户设置的 `o.discardTs`
- 非 managed：返回 `readMark.DoneUntil()`

含义：

- **<= discardTs 的版本，不再可能被任何活跃读事务看到**（或由用户保证），因此可以安全删除。

### 4.1 compaction 如何使用 discardTs

在 `levels.go:754` 之后：

- 仅当 `version <= discardTs`，才进入“版本计数与跳过策略”
- 结合 `NumVersionsToKeep`、`bitDiscardEarlierVersions`、过期/删除标记以及 overlap 判断，决定：
  - 保留哪些版本写入新表
  - 丢弃哪些版本（并为 vlog GC 统计可回收字节）

### 4.2 长事务的影响

如果存在长时间运行的读事务：

- `readMark.DoneUntil()` 推进慢
- `discardTs` 变小
- compaction 无法清理大量旧版本
- 进而导致：
  - LSM 空间回收慢
  - DISCARD 统计累积慢
  - vlog GC 效果差

因此 Badger 强烈不建议长时间持有读写事务；读事务也应尽量短。

---

## 5. managed 模式（简述）

managed 模式下，上层应用自行指定读写 ts，并通过 `SetDiscardTs`（或等效接口）推进 `discardTs`。

优点：

- 可控性更强（例如做备份/复制/外部一致性协议）

风险：

- 上层如果错误推进 discardTs，可能导致 compaction 删除仍需要的版本，破坏一致性。

