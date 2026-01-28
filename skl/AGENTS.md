# SKL KNOWLEDGE BASE

**Generated:** 2026-01-21
**Context:** SkipList implementation used for Badger's MemTable.

## OVERVIEW
High-performance, lock-free, concurrent SkipList optimized for SSD-based LSM trees.

## STRUCTURE
```
skl/
├── arena.go   # Lock-free memory allocator for nodes/keys/values
├── skl.go     # Core SkipList logic (Put, Get, Iterators)
└── README.md  # Benchmarks and node pooling details
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| **Write Path** | `skl.go` -> `Put` | Uses CAS for lock-free insertion and tower updates. |
| **Read Path** | `skl.go` -> `Get` | Uses `findNear` for O(log N) lookup. |
| **Allocations** | `arena.go` | Manages raw byte slices to minimize GC pressure. |
| **Concurrency** | `skl.go` | Heavy use of `sync/atomic` for height and towers. |
| **Iteration** | `skl.go` -> `Iterator` | Supports forward/backward and seek operations. |

## CONVENTIONS
- **Arena Allocation**: All data (nodes, keys, values) must be allocated via the `Arena`. Offset 0 is reserved as `nil`.
- **Lock-Free**: Operations use CAS (`CompareAndSwap`) instead of mutexes for scaling under high concurrency.
- **Node Alignment**: Nodes are 64-bit aligned (via `nodeAlign`) to ensure atomic operations on the `value` field work correctly on all architectures.
- **WiscKey optimization**: Only small metadata is stored in the LSM; keys/values in SkipList are often offsets or references.
- **Memory Safety**: Returned byte slices are often pointers into the Arena buffer. DO NOT modify them; copy if persistence is needed.
- **Reference Counting**: `IncrRef`/`DecrRef` used to manage SkipList lifecycle (often tied to MemTable flushing).
