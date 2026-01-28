# Badger 前后台线程（goroutine）调度与管线

本文把 Badger 的关键 goroutine、channel、背压点串起来，方便从“系统”角度理解读写、flush、compaction、GC 的协作。

相关代码主要在 `db.go`, `levels.go`, `value.go`, `memtable.go`。

---

## 1. 总体管线（写入）

一个写事务（Txn commit）大体会经历：

1) 申请 commitTs（oracle）
2) 把写请求（request）投递到 `db.writeCh`
3) `db.doWrites` 串行消费 request：
   - 对大 value：写入 vlog，得到 vptr
   - 把 kv（value 或 vptr）写入 memtable（含 WAL）
4) memtable 满/切换：旧 memtable 变 immutable，投递到 `db.flushChan`
5) `flushMemtable` 把 immutable memtable 刷成 L0 table
6) 后台 compactor 持续把 L0/Li 合并到更低层

关键特点：

- **写路径串行化**（单 doWrites）：避免 vlog/memtable 并发写复杂度
- **flush/compaction 后台化**：写线程只负责把数据进入 memtable/vlog
- **channel 背压**：memtable/flushChan 满时会反向阻塞写入

### 1.2 doWrites 详解

`doWrites`（`db.go:910`）是写入管线的核心 goroutine，负责串行消费 `writeCh` 中的写请求。

#### 整体结构

```
┌─────────────────────────────────────────────────────────────────────┐
│                         两个 goroutine 协作                         │
├─────────────────────────────────────────────────────────────────────┤
│  doWrites 主循环:         writeRequests 异步 goroutine:             │
│  持续从 writeCh 收集      执行 vlog + memtable 写入                 │
│         │                            │                              │
│         └──── pendingCh 同步点 ──────┘                              │
│         (同一时刻只有一批在执行, pendingCh有元素表示正在写)         │
└─────────────────────────────────────────────────────────────────────┘
```

#### 时间线：两 goroutine 如何协作

```
时间 →
t0: doWrites 收集第一批请求
t1: doWrites 发送 pendingCh <- struct{}{} (成功，channel 空)
t2: go writeRequests(reqs) 启动异步写入 goroutine
t3: doWrites 继续收集下一批 (不等待 writeRequests 完成)
t4: doWrites 尝试 pendingCh <- struct{}{}
    → 阻塞！因为 channel 还被第一批占用
t5: 第一批 writeRequests 完成:
    - 写入 vlog
    - 写入 memtable
    - 执行 <-pendingCh (释放 channel)
t6: doWrites 的 pendingCh 发送成功，启动第二批
    → 循环重复...
```

`pendingCh` 相当于信号量，在这里可以控制并发。

#### 核心代码

```go
func (db *DB) doWrites(lc *z.Closer) {
    defer lc.Done()
    pendingCh := make(chan struct{}, 1)  // 容量=1，同步点

    writeRequests := func(reqs []*request) {
        db.writeRequests(reqs)   // 写入 vlog + memtable (I/O 操作)
        <-pendingCh               // 完成后释放同步点
    }

    reqs := make([]*request, 0, 10)
    for {
        // 1) 等待第一个请求
        var r *request
        select {
        case r = <-db.writeCh:
        case <-lc.HasBeenClosed():
            goto closedCase
        }

        // 2) 批量收集更多请求
        for {
            reqs = append(reqs, r)

            // 批次过大强制 flush
            if len(reqs) >= 3*kvWriteChCapacity {
                pendingCh <- struct{}{}   // 尝试获取同步点 (可能阻塞)
                goto writeCase
            }

            select {
            case r = <-db.writeCh:        // 继续收集
            case pendingCh <- struct{}{}: // 上一批完成，可以开始新一批
                goto writeCase
            case <-lc.HasBeenClosed():
                goto closedCase
            }
        }

    writeCase:
        go writeRequests(reqs)           // 异步执行 (关键！)
        reqs = make([]*request, 0, 10)  // 立即重置，继续收集下一批
    }
}
```

#### 关键设计点

**1. pendingCh：同步点，流控**

```
pendingCh (容量=1)
    │
    ├── doWrites: pendingCh <- struct{}{}  (发送者，阻塞等待)
    │
    └── writeRequests: <-pendingCh         (接收者，完成时释放)
```

**2. 为什么 `go writeRequests` 要异步？**

```
如果不异步 (go writeRequests):              实际设计 (go writeRequests):

doWrites 收集                               doWrites 收集
    ↓                                             ↓
doWrites 等待 I/O 完成                          go writeRequests 异步启动
    ↓                                             ↓
doWrites 继续收集下一批                        doWrites 立即继续收集下一批
    ↓                                             ↓
...浪费 CPU 时间...                           writeRequests 并行执行 I/O
```

- doWrites 专注**收集**，不等待 I/O
- writeRequests 在**独立 goroutine** 中执行 I/O
- 两 goroutine **并行工作**，提高吞吐

**3. 批量触发条件**

| 条件 | 行为 |
|------|------|
| 超过 `3 * kvWriteChCapacity` | 强制 flush（防止内存爆炸） |
| `pendingCh` 可写（上一批完成） | 立即开始新一批 |

**4. 优雅退出**

```go
closedCase:
    for {
        select {
        case r = <-db.writeCh:        // drain 剩余请求
            reqs = append(reqs, r)
        default:
            pendingCh <- struct{}{}   // 确保同步
            writeRequests(reqs)       // 同步写入 (不用 go)
            return
        }
    }
```

- **非阻塞 drain**：`default` 分支确保清空 writeCh
- **同步写入**：最后一批不用 `go`，确保落盘后才退出

---

## 2. Open 时启动的 goroutine 一览

见 `db.go:320`~`db.go:410`（非 ReadOnly）：

- `go db.updateSize(...)`：周期性统计 DB size
- `db.lc.startCompact(...)`：启动多个 compactor goroutine
- `go db.flushMemtable(...)`：flush goroutine（消费 `flushChan`）
- `go db.doWrites(...)`：写入 goroutine（消费 `writeCh`）
- `go db.vlog.waitOnGC(...)`：关闭时阻塞/等待 GC（不是周期性 GC）
- `go db.pub.listenForUpdates(...)`：publisher（stream 等）
- `go db.threshold.listenForValueThresholdUpdate()`：动态阈值

这些 goroutine 通过 `db.closers.*` 统一收敛。

### 2.1 Closer：goroutine 生命周期管理

`Closer`（来自 `ristretto/v2/z`）是 Badger 用于管理后台 goroutine 优雅退出的机制。

#### 基本使用模式

```go
// 1. 创建 Closer（参数 n 表示期望调用 Done() 的次数，通常是 1）
closer := z.NewCloser(1)

// 2. 启动 goroutine，传入 Closer
go myWorker(closer)

// 3. goroutine 内部
func myWorker(lc *z.Closer) {
    defer lc.Done()                      // ← 退出时必须调用！

    for {
        select {
        case <-lc.HasBeenClosed():        // ← 收到退出信号
            return                        //    defer Done() 被执行
        case task := <-taskCh:
            // 处理任务...
        }
    }
}

// 4. 外部通知退出
closer.SignalAndWait()                   // Signal() + 等待 Done()
```

#### 关键方法

| 方法 | 行为 |
|------|------|
| `NewCloser(n)` | 创建，n 表示期望的 Done() 调用次数 |
| `HasBeenClosed()` | 返回 `chan struct{}`，用于 `select` 监听退出信号 |
| `Signal()` | 发送退出信号（关闭 channel） |
| `Done()` | goroutine 退出时调用，减少计数 |
| `SignalAndWait()` | 发送信号 + 阻塞等待所有 Done() |

#### HasBeenClosed() 返回什么？

```go
case <-lc.HasBeenClosed():  // 这是一个 channel
```

**返回 `context.Context.Done()` channel**。

实际实现（`ristretto/v2/z/z.go`）：

```go
type Closer struct {
    waiting sync.WaitGroup    // WaitGroup，等待 goroutine 退出
    ctx    context.Context    // context，用于发送退出信号
    cancel context.CancelFunc // cancel 函数
}

func (lc *Closer) HasBeenClosed() <-chan struct{} {
    return lc.ctx.Done()  // 返回 context 的 Done() channel
}

func (lc *Closer) Signal() {
    lc.cancel()  // 调用 cancel，关闭 ctx.Done()
}

func (lc *Closer) Done() {
    lc.waiting.Done()  // WaitGroup.Done()
}

func (lc *Closer) Wait() {
    lc.waiting.Wait()  // WaitGroup.Wait()
}
```

**工作流程**：

1. `NewCloser(1)` 创建 `context.WithCancel()`
2. `HasBeenClosed()` 返回 `ctx.Done()` channel
3. `Signal()` 调用 `cancel()`，关闭 `ctx.Done()` channel
4. `Done()` / `Wait()` 操作底层的 `sync.WaitGroup`

#### Signal vs SignalAndWait

| 方法 | 行为 |
|------|------|
| `Signal()` | 发送退出信号，立即返回 |
| `SignalAndWait()` | 发送信号 + 等待所有 `Done()` 被调用 |

```go
// SignalAndWait 的典型场景：按顺序关闭多个 goroutine
db.closers.valueGC.SignalAndWait()   // 先停 GC
db.closers.writes.SignalAndWait()    // 再停写入
db.closers.pub.SignalAndWait()       // 最后停发布
```

#### "waiting" 是什么意思？

`SignalAndWait()` 中的 "waiting" 指**等待 goroutine 退出**：

```
主线程                    doWrites goroutine
   │                           │
   │  SignalAndWait()          │
   │  ──────────────────────→  │  Signal() 关闭 channel
   │                           │
   │  (阻塞等待...)             │  HasBeenClosed() 可读
   │                           │
   │                           │  收到信号，开始清理
   │                           │
   │                           │  return
   │                           │  defer Done() 执行
   │  ←──────────────────────  │
   │  (返回，继续执行)          │
```

#### doWrites 中的完整示例

```go
// db.go:378-379 - 创建并启动
db.closers.writes = z.NewCloser(1)
go db.doWrites(db.closers.writes)

// db.go:910-932 - goroutine 内部
func (db *DB) doWrites(lc *z.Closer) {
    defer lc.Done()                    // ← 退出时调用

    for {
        select {
        case r = <-db.writeCh:
            // 处理写入...
        case <-lc.HasBeenClosed():      // ← 收到退出信号
            goto closedCase             //    清理后返回
        }
    }
closedCase:
    // 清理剩余请求...
    return  // defer Done() 自动执行
}

// db.go:547 - 外部通知退出（DB.Close 时）
db.closers.writes.SignalAndWait()      // 阻塞等待 doWrites 退出
```

#### 为什么 NewCloser(1)？

参数 `n` 表示**期望调用 `Done()` 的次数**：

```go
closer := z.NewCloser(1)   // 1 个 goroutine
closer := z.NewCloser(3)   // 3 个 goroutine，每个都调用 Done()

go worker1(closer)  // defer Done()
go worker2(closer)  // defer Done()
go worker3(closer)  // defer Done()

closer.SignalAndWait()  // 等待 3 个 Done() 后才返回
```

Badger 中绝大多数情况都是 `NewCloser(1)`，因为每个 Closer 管理一个 goroutine。

#### blockWrite/unblockWrite 中的 Closer 替换

```go
// db.go:1635 - 关闭 doWrites
db.closers.writes.SignalAndWait()  // 旧 Closer，doWrites 退出

// db.go:1641-1642 - 创建新 Closer，重启 doWrites
db.closers.writes = z.NewCloser(1)
go db.doWrites(db.closers.writes)
```

**关键点**：`blockWrite` 后必须创建**新的 Closer**，不能复用旧的！

---

## 3. 关键 channel 与背压

### 3.1 writeCh：写入串行化

- `db.writeCh chan *request`（`db.go:103`）
- 由事务提交路径投递（`db.writeCh <- req`）
- `db.doWrites` 循环消费

背压：

- writeCh 容量有限（`kvWriteChCapacity`），爆满时提交会阻塞。

### 3.2 flushChan：memtable flush 队列

- `db.flushChan chan *memTable`（`db.go:104`）

写线程在切换 memtable 时会把旧 memtable 投递到 flushChan。

背压：

- flushChan 容量通常为 `NumMemtables`
- 当 flushChan 满：写线程无法切换 memtable，最终会阻塞写入

这是 Badger 的核心稳定机制：

- 如果 flush 跟不上（磁盘慢/compaction 太忙），系统会自然降速，避免无限制堆内存。

### 3.3 memtable 生命周期

memtable 是 Badger 的内存写入缓冲区，理解它的生命周期对理解写入流程很重要。

#### 状态流转

```
┌─────────────┐     满了     ┌─────────────┐    flush    ┌─────────────┐
│   ACTIVE    │ ──────────→ │  IMMUTABLE  │ ──────────→ │   L0 SST    │
│  (db.mt)    │             │   (db.imm)  │             │   (磁盘)     │
└─────────────┘             └─────────────┘             └─────────────┘
     ↓ 写入                      ↓ 等待 flush                ↓ 可被 compaction
   memtable.Put              flushChan 排队
```

#### 关键代码路径

**1. 创建与初始化** (`db.go:330`, `db.go:1026`)

```go
// DB Open 时创建第一个 memtable
db.mt, err = db.newMemTable()

// memtable 满时切换
func (db *DB) ensureRoomForWrite() error {
    if !db.mt.isFull() {
        return nil  // 还有空间，继续用
    }

    // 尝试投递到 flushChan
    select {
    case db.flushChan <- db.mt:
        db.imm = append(db.imm, db.mt)  // 加入 immutable 列表
        db.mt, err = db.newMemTable()   // 创建新的 memtable
        return nil
    default:
        return errNoRoom  // flushChan 满了，需要等待
    }
}
```

**2. 写入** (`memtable.go:170`)

```go
func (mt *memTable) Put(key []byte, value y.ValueStruct) error {
    // 1. 写 WAL
    if mt.wal != nil {
        mt.wal.writeEntry(mt.buf, entry, mt.opt)
    }

    // 2. 写 Skiplist
    mt.sl.Put(key, value)

    // 3. 检查是否满（下次 ensureRoomForWrite 会触发切换）
    return nil
}
```

**3. Flush** (`db.go:1097-1128`)

```go
func (db *DB) flushMemtable(lc *z.Closer) {
    defer lc.Done()

    for mt := range db.flushChan {
        // 1. 遍历 skiplist，构建 SST
        if err := db.handleMemTableFlush(mt, nil); err != nil {
            // 失败重试
            time.Sleep(time.Second)
            continue
        }

        // 2. 从 imm 列表移除
        db.lock.Lock()
        y.AssertTrue(mt == db.imm[0])  // 必须是队首
        db.imm = db.imm[1:]
        mt.DecrRef()  // 释放内存
        db.lock.Unlock()
    }
}

func (db *DB) handleMemTableFlush(mt *memTable, dropPrefixes [][]byte) error {
    // 1. 创建 skiplist 迭代器
    itr := mt.sl.NewUniIterator(false)

    // 2. 构建 SST
    builder := buildL0Table(itr, nil, bopts)

    // 3. 写入磁盘
    fileID := db.lc.reserveFileID()
    tbl, err := table.CreateTable(table.NewFilename(fileID, db.opt.Dir), builder)

    // 4. 加入 L0
    return db.lc.addLevel0Table(tbl)
}
```

#### 关键数据结构

| 字段 | 类型 | 作用 |
|------|------|------|
| `db.mt` | `*memTable` | 当前活跃的 memtable，接收写入 |
| `db.imm` | `[]*memTable` | immutable memtables 列表，等待 flush |
| `db.flushChan` | `chan *memTable` | flush 任务队列 |

#### 并发安全

| 操作 | 需要锁保护 | 说明 |
|------|-----------|------|
| 写入 `db.mt` | ❌ | doWrites 单 goroutine，串行写入 |
| 切换 memtable | `db.lock.Lock()` | `ensureRoomForWrite` 需要修改 `db.imm` |
| Flush | `db.lock.Lock()` | `flushMemtable` 需要从 `db.imm` 移除 |
| 读取（Get/Iterator） | ❌ | 通过引用计数 (`IncrRef/DecrRef`) |

#### flushChan 背压机制

```
写入线程                flushMemtable
   │                         │
   │  ensureRoomForWrite      │
   │  ──────────────────→    │
   │                         │
   │  flushChan <- mt        │
   │  (成功)                 │
   │                         │
   │  继续写入                │  从 flushChan 取出
   │                         │
   │  ensureRoomForWrite      │
   │  ──────────────────→    │
   │                         │
   │  flushChan <- mt        │
   │  (flushChan 满了!)       │
   │                         │
   │  阻塞等待...             │  继续处理
   │  (背压生效)              │
   │                         │
   │  ←───────────────────   │  完成一个 flush
   │  (可以继续)              │
```

**关键点**：`flushChan` 满时，写入线程无法切换 memtable，最终会阻塞写入，形成自然的背压。

---

## 4. compaction 并行模型

`levelsController.startCompact` 会启动 N 个 compactor（N 由配置决定）。每个 compactor 循环：

1) `pickCompactLevels` 评估各层 score
2) `doCompact`

在 `subcompact` 内部还会：

- 将一次 compaction 按 key ranges 切分成多个 sub-compaction
- 每个 sub-compaction 独立构建 table.Builder
- 构建 table 过程通过 goroutine 并行，但受 `y.Throttle` 限制（避免过度并行）

因此 compaction 有两层并行：

- 多 compactor 并行处理不同层/不同任务
- 单 compaction 内部按 key range 并行建表

---

## 5. value log GC 的“线程模型”

- GC 不是常驻循环，需要用户主动调用 `DB.RunValueLogGC`。
- 运行时互斥通过 `valueLog.garbageCh`（容量 1）实现。
- DB Close 时通过 `waitOnGC` 阻止新 GC 并等待当前 GC 结束。

GC 与后台 compaction 的耦合点：

- compaction 会累加 DISCARD 统计（哪些 vlog 文件垃圾多）
- GC rewrite 会引入额外写入与 LSM 更新，可能带来 compaction 活动峰值

---

## 6. 读路径与后台任务的交互（关键点）

- 读事务的 `readTs` 通过 oracle watermark 管理。
- compaction 的版本清理边界由 `discardTs = orc.discardAtOrBelow()` 决定。

因此：

- **长时间运行的读事务**会拉低 `discardTs`，导致 compaction 无法删除旧版本，空间回收变慢。
- 同时 DISCARD 统计也更难累积，vlog GC 的效果会变差。
