# BADGER KNOWLEDGE BASE

**Generated:** 2026-01-21
**Context:** Fast, embeddable key-value DB in Go (WiscKey paper implementation).

## OVERVIEW
Badger is a persistent, embeddable key-value store optimized for SSDs. It separates keys (LSM tree) from values (Value Log) to reduce write amplification. Supports concurrent ACID transactions with Serializable Snapshot Isolation (SSI).

## STRUCTURE
```
.
├── badger/       # CLI tool (backup, restore, stream)
├── table/        # SSTable (Sorted String Table) implementation
├── skl/          # SkipList implementation (MemTable underlying structure)
├── y/            # Core utilities, metrics, and shared primitives
├── pb/           # Protocol Buffers definitions
├── fb/           # FlatBuffers definitions
├── trie/         # Trie implementation for stream framework
├── db.go         # Main DB entry point and orchestration
├── value.go      # Value Log (vlog) implementation
├── levels.go     # LSM Tree Level Controller
└── iterator.go   # Merging iterator implementation
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| **Core Logic** | `db.go` | Open, Close, Sync, Transactions |
| **Write Path** | `db.go` -> `doWrites` | Batches writes, handles WAL, updates MemTable |
| **Read Path** | `txn.go` -> `Get` | Checks MemTable -> Immutable MemTables -> L0-L6 |
| **Compaction** | `compaction.go`, `levels.go` | L0->L1, Lx->Ly compactions |
| **MemTable** | `memtable.go`, `skl/` | SkipList-based in-memory write buffer |
| **Value Log** | `value.go` | Stores actual values; keys point here |
| **GC** | `value.go` -> `RunValueLogGC` | Garbage collection of value log files |
| **Manifest** | `manifest.go` | Tracks file versions and DB state changes |

## CODE MAP
| Symbol | Type | Location | Role |
|--------|------|----------|------|
| `DB` | Struct | `db.go` | **Central Hub**. Manages all subsystems (Manifest, Levels, Vlog). |
| `Txn` | Struct | `txn.go` | Transaction handle. Manages reads/writes with ACID guarantees. |
| `memTable` | Struct | `memtable.go` | In-memory buffer. Uses `skl` (SkipList) for sorting. |
| `Table` | Struct | `table/table.go` | Represents an on-disk SSTable. |
| `ValueStruct` | Struct | `y/iterator.go` | Internal representation of a KV pair (Key, Value, Meta, Version). |
| `Entry` | Struct | `badger` | Public API struct for setting Key/Value. |
| `Iterator` | Interface | `iterator.go` | Standard iterator interface for traversing DB. |

## CONVENTIONS
- **Error Handling**: Explicit checks. `y.Wrap(err)` used occasionally.
- **Concurrency**: Heavy use of `sync.RWMutex`.
- **Channels**: `writeCh` for write serialization, `flushChan` for MemTable flushing.
- **Testing**: `require` (testify) or `y.Assert` widely used. Use `runBadgerTest` wrapper for DB lifecycle.
- **Zero-Copy**: Critical for performance. Keys/Values often valid only during transaction/iterator life.

## ANTI-PATTERNS (THIS PROJECT)
- **Modifying byte slices**: Keys/Values returned by Badger are often references to mmap'd files. DO NOT modify them. Copy if needed.
- **Long-running Txn**: Read-Write transactions hold locks. Keep them short.
- **Blocking**: Avoid blocking operations in the write path loop.

## COMMANDS
```bash
# Run all tests
go test -v ./...

# Run Bank test (consistency check)
# Requires nightly build setup usually, but check test.sh
./test.sh

# Install CLI
cd badger && go install .
```

## GO-YCSB WORKFLOW
Use this when benchmarking local Badger code via the forked `go-ycsb` submodule.

```bash
# 0) Ensure submodule is present.
git submodule update --init --recursive

# 1) Modify Badger source in this repo (for example db.go/value.go/options.go), then optionally
#    do a quick compile check in Badger itself.
go test ./badger/cmd

# 2) Rebuild go-ycsb (it uses local Badger via:
#    replace github.com/dgraph-io/badger/v4 => ../.. in third_party/go-ycsb/go.mod)
go -C third_party/go-ycsb build ./cmd/go-ycsb

# 3) Run YCSB load + run against Badger backend.
go -C third_party/go-ycsb run ./cmd/go-ycsb load badger \
  -P workloads/workloada \
  -p badger.dir=/tmp/badger-ycsb \
  -p badger.valuedir=/tmp/badger-ycsb

go -C third_party/go-ycsb run ./cmd/go-ycsb run badger \
  -P workloads/workloada \
  -p badger.dir=/tmp/badger-ycsb \
  -p badger.valuedir=/tmp/badger-ycsb
```

Notes:
- Rebuild `go-ycsb` after each Badger code change to pick up local modifications.
- `third_party/go-ycsb` is a git submodule and can have its own commits.
- For other workloads, replace `workloads/workloada` with `workloadb`/`workloadc`/... .

## NOTES
- **WiscKey**: Understanding WiscKey paper is helpful. Keys are small (LSM), Values are large (Vlog).
- **Vlog GC**: Critical for reclaiming space. Moves valid values to new log files.
- **Closers**: `y.Closer` pattern used for clean shutdown of background goroutines.
