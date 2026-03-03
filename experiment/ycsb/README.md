# ycsb

Workflow experiment wrapper for running `go-ycsb` against local Badger code.

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

`experiment/Makefile` defaults to:

```bash
YCSB_SCENARIO=baseline
YCSB_CONFIG=experiment/ycsb/scenarios/$(YCSB_SCENARIO)/config.json
```

## Config

Pass a scenario config JSON path as the first argument:

```bash
go run ./experiment/ycsb ./experiment/ycsb/scenarios/baseline/config.json
```

Key fields in each `config.json`:

- `goYCSBDir`
- `goYCSBBinary`
- `workloadFile`
- `db`
- `badgerDir`
- `badgerValueDir`
- `goCache`
- `goModCache`
- `extraProperties`
- `loadProperties`
- `runProperties`

`workloadFile` supports:

- absolute path
- path relative to current working directory
- path relative to the scenario config directory (recommended, for example `workload`)
- path relative to `goYCSBDir` (backward compatibility)

Badger value log GC properties (set through `extraProperties`):

- `badger.value_log_gc_interval`: Go duration (`0s` disables GC; default `0s`).
- `badger.value_log_gc_discard_ratio`: float in `(0,1)` (default `0.5`).

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
# or
make -C experiment ycsb-all
```

Select another scenario:

```bash
make -C experiment ycsb-all YCSB_SCENARIO=my-scenario
```

Built-in scenarios:

- `baseline` (workload A)
- `workloada`
- `workloadb`
- `workloadc`
- `workloadd`
- `workloade`
- `workloadf`

Override config path directly:

```bash
make -C experiment ycsb-all YCSB_CONFIG=experiment/ycsb/scenarios/my-scenario/config.json
```
