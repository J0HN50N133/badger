# ycsb

Workflow wrapper for running `go-ycsb` against local Badger code.

This experiment reads all runtime parameters from a JSON config file and provides
stable `make` targets in `experiment/Makefile`.

## Scenario Layout

Use one directory per test scenario:

```bash
experiment/ycsb/scenarios/
  baseline/
    config.json
    workload
  my-scenario/
    config.json
    workload
```

`experiment/Makefile` defaults:

```bash
YCSB_SCENARIO=baseline
YCSB_CONFIG=experiment/ycsb/scenarios/$(YCSB_SCENARIO)/config.json
```

## Config Schema

Pass a scenario config JSON path as the first argument:

```bash
go run ./experiment/ycsb ./experiment/ycsb/scenarios/baseline/config.json
```

Top-level fields in `config.json`:

| Field | Required | Default | Description |
|---|---|---|---|
| `goYCSBDir` | yes | - | Path to `go-ycsb` source directory. |
| `goYCSBBinary` | no | `go-ycsb` | Binary output filename used by build phase and runtime. |
| `workloadFile` | yes | - | Workload file path passed to `go-ycsb -P`. |
| `db` | no | `badger` | Backend name for go-ycsb (`badger`, etc.). |
| `badgerDir` | yes | - | Badger LSM directory. |
| `badgerValueDir` | no | same as `badgerDir` | Badger value log directory. |
| `goCache` | no | `/tmp/badger-gocache` | `GOCACHE` for build/load/run commands. |
| `goModCache` | no | `/tmp/badger-gomodcache` | `GOMODCACHE` for build/load/run commands. |
| `extraProperties` | no | `{}` | Common `-p key=value` properties for both `load` and `run`. |
| `loadProperties` | no | `{}` | Phase-only properties for `load`. |
| `runProperties` | no | `{}` | Phase-only properties for `run`. |

Property merge order:

1. Built-ins: `badger.dir`, `badger.valuedir`
2. `extraProperties`
3. Phase properties (`loadProperties` or `runProperties`)

Later entries override earlier entries.

Recommended split:

1. Put workload behavior in `workload` file:
   `recordcount`, `operationcount`, `threadcount`, and read/write/scan proportions.
2. Keep phase-specific control in config:
   `loadProperties.dropdata=true`, and optional phase-only overrides.

`workloadFile` path resolution order:

1. absolute path
2. relative to scenario config directory
3. relative to `goYCSBDir`
4. fallback: relative to current working directory

Recommended: put `workload` in the scenario folder and set `"workloadFile": "workload"`.

## YCSB Properties

These are passed through to `go-ycsb` via `-p key=value`.

`workload` file format:

- One property per line: `key=value`
- `#` starts a comment line
- No JSON/YAML structure, just flat properties

Priority rule:

1. Properties from `workload` file (`-P`)
2. Properties passed via `-p key=value`

`-p` overrides the same key from `workload`.

### Workload File Properties

Built-in scenario `workload` files currently use these keys:

| Key | Meaning | Notes |
|---|---|---|
| `workload` | Workload implementation name. | Usually `core`. |
| `recordcount` | Number of initial records. | Used by load and workload generator range. |
| `operationcount` | Number of operations in run phase. | Total ops across all threads. |
| `threadcount` | Number of concurrent YCSB workers. | Applies if not overridden in `loadProperties/runProperties`. |
| `readallfields` | Read all fields in each record. | Usually `true` for standard A-F. |
| `readproportion` | Fraction of read operations. | Together with other proportions defines mix. |
| `updateproportion` | Fraction of update operations. | Together with other proportions defines mix. |
| `insertproportion` | Fraction of insert operations. | Together with other proportions defines mix. |
| `scanproportion` | Fraction of scan operations. | Together with other proportions defines mix. |
| `readmodifywriteproportion` | Fraction of read-modify-write operations. | Used by workload F. |
| `requestdistribution` | Key selection distribution. | Common values: `uniform`, `zipfian`, `latest`. |
| `maxscanlength` | Upper bound of scan length. | Effective when `scanproportion > 0`. |
| `scanlengthdistribution` | Distribution for scan length. | Example: `uniform`. |

### Common Override Properties

These are often set in `config.json` as `extraProperties` / phase properties:

| Key | Typical phase | Meaning |
|---|---|---|
| `recordcount` | load/run | Initial record count for workload generator. |
| `operationcount` | run | Number of operations in run phase. |
| `threadcount` | load/run | Number of worker threads. |
| `dropdata` | load | Whether to clear DB before load (`true`/`false`). |
| `measurementtype` | extra | Measurement type, usually `histogram` or `raw`. |
| `fieldcount` | workload file / properties | Number of fields per record. |
| `fieldlength` | workload file / properties | Field length in bytes. |
| `readproportion` | workload file / properties | Fraction of read ops. |
| `updateproportion` | workload file / properties | Fraction of update ops. |
| `insertproportion` | workload file / properties | Fraction of insert ops. |
| `scanproportion` | workload file / properties | Fraction of scan ops. |
| `readmodifywriteproportion` | workload file / properties | Fraction of RMW ops. |
| `requestdistribution` | workload file / properties | Access distribution (`uniform`, `zipfian`, `latest`). |
| `table` | workload file / properties | Logical table name, default `usertable`. |

For this repository's built-in scenarios, these keys are intentionally stored in each scenario's
`workload` file by default:

- `recordcount`
- `operationcount`
- `threadcount`

More YCSB keys are defined in:

- `third_party/go-ycsb/pkg/prop/prop.go`
- `third_party/go-ycsb/workloads/workload*`

## Badger-Specific Properties

Supported by `third_party/go-ycsb/db/badger/db.go`:

| Key | Default | Meaning |
|---|---|---|
| `badger.dir` | from `badgerDir` | LSM directory. |
| `badger.valuedir` | from `badgerValueDir` | Value log directory. |
| `badger.sync_writes` | `false` | Sync writes to disk on each write. |
| `badger.num_versions_to_keep` | `1` | Number of versions kept per key. |
| `badger.max_table_size` | Badger default | Base table size. |
| `badger.level_size_multiplier` | `10` | Level size multiplier. |
| `badger.max_levels` | `7` | Max LSM levels. |
| `badger.value_threshold` | Badger default | Threshold for value log indirection. |
| `badger.num_memtables` | `5` | Number of memtables. |
| `badger.num_level0_tables` | `5` | L0 table count trigger. |
| `badger.num_level0_tables_stall` | `15` | L0 stall trigger. |
| `badger.level_one_size` | Badger default | Base level size. |
| `badger.value_log_file_size` | `1<<30` | Value log file size. |
| `badger.value_log_max_entries` | `1000000` | Max entries in one value log file. |
| `badger.num_compactors` | `3` | Number of compactors. |
| `badger.value_log_gc_interval` | `0s` | Value log GC interval (`0s` disables). |
| `badger.value_log_gc_discard_ratio` | `0.5` | Discard ratio for value log GC. |

## Run

Supported phases: `build`, `load`, `run`, `all` (default).

```bash
# build + load + run
go run ./experiment/ycsb ./experiment/ycsb/scenarios/baseline/config.json

# build only
go run ./experiment/ycsb ./experiment/ycsb/scenarios/baseline/config.json build

# load only
go run ./experiment/ycsb ./experiment/ycsb/scenarios/baseline/config.json load

# run only
go run ./experiment/ycsb ./experiment/ycsb/scenarios/baseline/config.json run
```

Preferred make workflow:

```bash
make -C experiment ycsb-build
make -C experiment ycsb-load
make -C experiment ycsb-run
make -C experiment ycsb-all
```

Select another scenario:

```bash
make -C experiment ycsb-all YCSB_SCENARIO=workloadf
```

Override config path directly:

```bash
make -C experiment ycsb-all YCSB_CONFIG=experiment/ycsb/scenarios/my-scenario/config.json
```

## Matrix Automation Script

Use `experiment/ycsb/run_matrix.py` to batch-run scenario and parameter matrices.

The script has a `DEFAULTS` block at the top of the file. You can edit those
defaults directly for your daily runs, then optionally override specific values
via CLI flags.

Example:

```bash
python3 experiment/ycsb/run_matrix.py \
  --scenarios workloada,workloadb,workloadf \
  --value-sizes 128,256,512,1024 \
  --recordcount 1000000 \
  --operationcount 1000000 \
  --threadcount 16 \
  --badger-dir-root /data/ycsb-lsm \
  --badger-value-dir-root /data/ycsb-vlog \
  --output-dir experiment/ycsb/results
```

What it does:

- Runs selected scenarios.
- Fixes key shape to 16B by default (`keyprefix=""`, `zeropadding=16`, `insertorder=ordered`).
- Sweeps value sizes via `--value-sizes` (`fieldlength`, with `fieldcount=1` and constant distribution).
- Allows overriding YCSB knobs (`recordcount`, `operationcount`, `threadcount`, and `--ycsb-prop`).
- Writes generated workload/config/logs under output directory grouped by:
  `run_id/scenario/value+recordcount+operationcount+threadcount`.
