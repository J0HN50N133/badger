# YCSB Scenarios

Create one directory per YCSB test scenario:

```text
experiment/ycsb/scenarios/<scenario>/
  config.json
  workload
```

Convention:

- Put workload behavior knobs (`recordcount`, `operationcount`, `threadcount`, proportions) in
  `workload`.
- Keep `config.json` focused on environment/backend/phase overrides (for example `dropdata`).

Built-in scenarios:

- `experiment/ycsb/scenarios/baseline/config.json`
- `experiment/ycsb/scenarios/workloada/config.json`
- `experiment/ycsb/scenarios/workloadb/config.json`
- `experiment/ycsb/scenarios/workloadc/config.json`
- `experiment/ycsb/scenarios/workloadd/config.json`
- `experiment/ycsb/scenarios/workloade/config.json`
- `experiment/ycsb/scenarios/workloadf/config.json`

Full config reference (including YCSB property descriptions):

- `experiment/ycsb/README.md`

Run with Makefile:

```bash
make -C experiment ycsb-all YCSB_SCENARIO=baseline
make -C experiment ycsb-all YCSB_SCENARIO=workloadf
```
