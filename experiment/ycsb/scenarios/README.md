# YCSB Scenarios

Create one directory per YCSB test scenario:

```text
experiment/ycsb/scenarios/<scenario>/config.json
```

Example:

- `experiment/ycsb/scenarios/baseline/config.json`

Run with Makefile:

```bash
make -C experiment ycsb-all YCSB_SCENARIO=baseline
```
