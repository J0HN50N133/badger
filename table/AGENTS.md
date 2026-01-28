# AGENTS: TABLE

**Context:** Core SSTable (Sorted String Table) implementation for LSM tree storage.

## OVERVIEW
The `table` package implements the on-disk SSTable format, including building tables from sorted entries, mmap-based reading, and efficient iteration with block-level caching and bloom filters.

## STRUCTURE
```
table/
├── builder.go        # Concurrent block building, compression, and encryption
├── iterator.go       # Bidirectional SSTable and Concat iterators
├── merge_iterator.go # Merging multiple iterators with heap-based ordering
├── table.go          # SSTable structure, block management, and checksums
└── Options.go        # Configuration for bloom filters, block size, and cache
```

## WHERE TO LOOK
| Component | Primary Logic | Role |
|-----------|---------------|------|
| **Builder** | `builder.go` | Batches entries into blocks; handles Snappy/ZSTD and AES encryption. |
| **Table** | `table.go` | Manages `.sst` files via mmap; handles index and bloom filter loading. |
| **Iterator** | `iterator.go` | `blockIterator` for internal blocks; `Iterator` for table-wide traversal. |
| **Block** | `table.go` | Unit of data for compression/caching; contains entries and checksums. |
| **Index** | `table.go` | FlatBuffer-based `TableIndex` stores block offsets and max version. |

## CONVENTIONS
- **Block Structure**: Entries are prefix-compressed (overlap/diff) within blocks. Each block ends with entry offsets, block checksum, and their lengths.
- **Checksums**: CRC32C is used for both individual blocks and the global table index.
- **Memory Management**: Uses `z.Allocator` for zero-copy building and `ristretto` for block/index caching.
- **Encryption**: If enabled, blocks and index are encrypted with AES-CTR; IV is appended to the data.
- **Reference Counting**: Tables and Blocks use atomic refcounts to manage mmap lifecycle and cache eviction.

## SSTABLE LAYOUT
```text
[Block 1][Block 2]...[Block N][Index][Index Size][Checksum][Checksum Size]
```
*Note: Each block has its own checksum and entry offsets footer.*
