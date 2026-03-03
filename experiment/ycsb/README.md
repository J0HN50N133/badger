# ycsb

Workflow experiment wrapper for running `go-ycsb` against local Badger code.

This experiment reads all runtime parameters from a JSON config file and provides
stable `make` targets in `experiment/Makefile`.

## Config

Pass a JSON file path as the first argument:

```bash
go run ./experiment/ycsb ./experiment/ycsb/config.example.json
```

Key fields:

- `goYCSBDir`: Path to go-ycsb submodule (for this repo, `third_party/go-ycsb`).
- `goYCSBBinary`: Output binary name produced by build phase.
- `workloadFile`: Workload file path relative to `goYCSBDir`.
- `db`: Database backend name (use `badger`).
- `badgerDir`: LSM directory used by go-ycsb Badger backend.
- `badgerValueDir`: Value-log directory (defaults to `badgerDir` if empty).
- `goCache`: GOCACHE value used by build/load/run commands.
- `goModCache`: GOMODCACHE value used by build/load/run commands.
- `extraProperties`: Common `-p key=value` properties applied to both phases.
- `loadProperties`: Extra properties only for `load` phase.
- `runProperties`: Extra properties only for `run` phase.

## Run

Supported phases: `build`, `load`, `run`, `all` (default).

```bash
# build + load + run
go run ./experiment/ycsb ./experiment/ycsb/config.example.json

# build only
go run ./experiment/ycsb ./experiment/ycsb/config.example.json build

# load only
go run ./experiment/ycsb ./experiment/ycsb/config.example.json load

# run only
go run ./experiment/ycsb ./experiment/ycsb/config.example.json run
```

Preferred make workflow:

```bash
make -C experiment ycsb-build
make -C experiment ycsb-load
make -C experiment ycsb-run
# or
make -C experiment ycsb-all
```

Override config path:

```bash
make -C experiment ycsb-all YCSB_CONFIG=experiment/ycsb/config.example.json
```
