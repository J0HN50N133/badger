# AGENTS: y

**Generated:** 2026-01-21
**Context:** Shared utilities, core primitives, and performance-critical metrics.

## OVERVIEW
Package `y` provides low-level, high-performance utilities and shared primitives used across all Badger subsystems.

## STRUCTURE
```
.
├── bloom.go        # Bloom filter implementation
├── checksum.go     # CRC32 checksum utilities
├── encrypt.go      # Encryption at rest primitives
├── error.go        # Assertion and error wrapping (Check, Assert, Wrap)
├── iterator.go     # ValueStruct and Iterator interface definitions
├── metrics.go      # Global expvar-based performance metrics
├── watermark.go    # Transaction and flush index tracking
└── y.go            # Byte manipulation, file I/O, and Throttle
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| **Ordering** | `watermark.go` | `WaterMark` tracks min un-finished indices for SSI. |
| **KV Primitives** | `iterator.go` | `ValueStruct` (Key/Value/Meta/Version/Expiry). |
| **Error Handling** | `error.go` | `AssertTrue`, `Wrap`, and `Check` for fatal error paths. |
| **Telemetry** | `metrics.go` | `badger_` prefixed expvars for LSM, Vlog, and DB. |
| **Resource Control** | `y.go` | `Throttle` for worker pools; `PageBuffer` for zero-copy. |
| **Key Encoding** | `y.go` | `KeyWithTs`, `ParseTs`, `CompareKeys` (Versioned keys). |

## CONVENTIONS
- **Fatal on Bug**: Use `y.AssertTrue` for invariant checks that should crash if violated.
- **Error Wrapping**: Use `y.Wrap(err, "msg")` to add context without losing root cause.
- **Zero Allocation**: Prefer `y.Slice` or `y.PageBuffer` for reusable byte buffers.
- **Atomic Ordering**: `WaterMark` relies on serial `Begin`/`Done` calls to advance `DoneUntil`.
- **Versioned Keys**: All keys must have an 8-byte big-endian timestamp suffix (handled by `KeyWithTs`).
- **Metric Updates**: Always check `enabled` flag before updating metrics via `y.NumGetsAdd` etc.
