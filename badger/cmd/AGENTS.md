# BADGER CLI COMMANDS

**Generated:** 2026-01-21
**Context:** Command-line tools for managing and testing Badger databases.

## OVERVIEW
The `badger/cmd` package implements a Cobra-based CLI for offline maintenance, data movement, and consistency verification.

## STRUCTURE
```
.
├── root.go             # CLI entry point; handles global flags (--dir, --vlog-dir)
├── backup.go           # Version-agnostic backup to protocol buffers
├── restore.go          # Restore from protocol buffer backup files
├── info.go             # DB health check, manifest inspection, and key/table listing
├── bank.go             # Jepsen-inspired consistency and SSI verification test
├── stream.go           # Stream-based data export/import
├── flatten.go          # Forces compaction to flatten the LSM tree
├── bench.go            # Base command for benchmarking suite
├── write_bench.go      # Write performance benchmarking
├── read_bench.go       # Read performance benchmarking
└── pick_table_bench.go # SSTable access benchmarking
```

## WHERE TO LOOK
| Command | Implementation File | Purpose |
|---------|---------------------|---------|
| `badger backup` | `backup.go` | Export DB to `.bak` file using `db.Backup`. |
| `badger restore` | `restore.go` | Import from `.bak` file using `db.Load`. |
| `badger info` | `info.go` | Analyzes MANIFEST, SSTables, and Vlog for abnormalities. |
| `badger bank` | `bank.go` | Verifies ACID/SSI via concurrent "money transfers". |
| `badger flatten` | `flatten.go` | Triggers L0-Lmax compactions to reduce levels. |
| `badger stream` | `stream.go` | Demonstrates/tests the Stream framework for data migration. |
| `badger benchmark`| `*_bench.go` | Performance measurement for various workloads. |

## CONVENTIONS
- **Cobra Integration**: Every command is a `*cobra.Command` that registers itself to `RootCmd` in `init()`.
- **Global Directory Flags**: Commands inherit `--dir` and `--vlog-dir` from `root.go` for DB location.
- **Direct DB Access**: Most commands open the DB directly (`badger.Open` or `badger.OpenManaged`) and should be run when no other process is using the database.
- **Protocol Buffers**: Backup/Restore formats use the `pb` package for cross-version compatibility.
- **Managed Mode**: `bank disect` uses `OpenManaged` for precise timestamp/version-based inspection.
