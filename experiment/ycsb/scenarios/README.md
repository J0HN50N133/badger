# YCSB Scenarios

Create one directory per YCSB test scenario:

```text
experiment/ycsb/scenarios/<scenario>/
  config.json
  workload
```

Built-in scenarios:

- `experiment/ycsb/scenarios/baseline/config.json`
- `experiment/ycsb/scenarios/workloada/config.json`
- `experiment/ycsb/scenarios/workloadb/config.json`
- `experiment/ycsb/scenarios/workloadc/config.json`
- `experiment/ycsb/scenarios/workloadd/config.json`
- `experiment/ycsb/scenarios/workloade/config.json`
- `experiment/ycsb/scenarios/workloadf/config.json`

Run with Makefile:

```bash
make -C experiment ycsb-all YCSB_SCENARIO=baseline
make -C experiment ycsb-all YCSB_SCENARIO=workloadf
```
