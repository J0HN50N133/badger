# Value-Log Hot-Tier Eviction Policy Design

## Scope
This document captures the current design and implementation details of value-log hot-tier eviction (offload) policies.

It reflects the behavior implemented in:
- `vlog_offload_policy.go`
- `value.go` (policy event hooks and rotation trigger)
- `object_storage_vlog_test.go` (policy and E2E tests)

## Goals
- Keep policy logic pluggable via an interface.
- Let each policy own and maintain its own internal state.
- Keep `valueLog` unaware of policy internals.
- Trigger offload decisions when vlog rotates (new writable vlog created).
- Keep behavior deterministic across restart/recovery for unknown local fids.

## Interface
`ValueLogOffloadPolicy` exposes four methods:

- `OnLocalFileCreated(fid uint32)`
- `OnLocalFileRead(fid uint32)`
- `OnLocalFileDeleted(fid uint32)`
- `DecideOffload(ctx ValueLogOffloadContext) []ValueLogOffloadDecision`

### Context and Decision
- `ValueLogOffloadContext`
  - `NewWritableFid`
  - `MaxFid`
  - `LocalFids`
- `ValueLogOffloadDecision`
  - `Fid`
  - `PruneLocal`

## Integration in ValueLog
`value.go` only sends lifecycle/read events to the policy and requests decisions. It does not store policy statistics.

### Event hooks
- On local vlog creation (open/create/hydrate): `policy.OnLocalFileCreated(fid)`
- On local vlog read: `policy.OnLocalFileRead(fid)`
- On local vlog deletion/prune/drop: `policy.OnLocalFileDeleted(fid)`

### Decision trigger
When write path rotates vlog (new writable file created), Badger calls policy:
1. Build `ValueLogOffloadContext` from local state.
2. Call `DecideOffload`.
3. Execute offload decisions best-effort via `offloadFid`.

## Implemented Policies

### FIFOValueLogOffloadPolicy
Data structures:
- `list.List`: global creation order (front oldest, back newest)
- `map[fid]*Element`: O(1) index for membership/removal

Behavior:
- Create: append to back.
- Read: no-op.
- Delete: remove from list/index.
- Decide: iterate list from front, pick closed fids in order.

Recovery/unknown fids:
- Unknown local fids are seeded deterministically by sorted fid order before decision.
- This preserves deterministic FIFO behavior across cycles.

### LRUValueLogOffloadPolicy
Data structures:
- `list.List`: recency order (front coldest, back hottest)
- `map[fid]*Element`: O(1) index

Behavior:
- Create: push back.
- Read: move to back.
- Delete: remove.
- Decide: iterate from front and select closed fids.

### LFUValueLogOffloadPolicy
Data structures:
- `map[fid]*node`
- `min-heap` of nodes keyed by:
  1. `accessCount` ascending
  2. `lastTouch` ascending
  3. `createdOrder` ascending
  4. `fid` ascending

Behavior:
- Create: insert node into heap with initial metadata.
- Read: increment `accessCount`, update touch counter, `heap.Fix`.
- Delete: `heap.Remove` and delete map entry.
- Decide: pop minimum candidates until enough closed fids are selected, then push popped nodes back.

## Determinism and Recovery Rules
- If policy misses historical events (restart/reopen), unknown local fids are discovered from context.
- FIFO/LRU/LFU each apply deterministic tie-breaking.
- FIFO explicitly seeds unknown local fids in sorted order to avoid map-iteration nondeterminism.

## Complexity (practical)
- FIFO:
  - create/delete: O(1)
  - decision: O(n) scan over list (plus small sorting for unknown seeding)
- LRU:
  - create/read/delete: O(1)
  - decision: O(n)
- LFU:
  - create/read/delete: O(log n)
  - decision: O(k log n) where k is number of files to evict

## Configuration
Use options:
- `WithValueLogOnObjectStorage(true)`
- `WithValueLogObjectStore(...)`
- `WithValueLogOffloadPolicy(...)`

Example policy wiring:
- `&FIFOValueLogOffloadPolicy{KeepLocalClosed: X, PruneLocal: true}`
- `&LRUValueLogOffloadPolicy{KeepLocalClosed: X, PruneLocal: true}`
- `&LFUValueLogOffloadPolicy{KeepLocalClosed: X, PruneLocal: true}`

## Tests
Policy behavior and integration are covered by:
- `TestHotTierEvictionPolicyFIFO`
- `TestHotTierEvictionPolicyFIFOUnknownFidsDeterministicAcrossCycles`
- `TestHotTierEvictionPolicyLRU`
- `TestHotTierEvictionPolicyLFU`
- `TestAutoOffloadOnRotateE2E`
- plus offload/hydrate E2E tests

## Non-goals (current MVP)
- No persistent policy-state snapshot file.
- No cost model beyond keep-N-closed objective.
- No direct GC/offload co-optimization yet.
