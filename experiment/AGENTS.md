# Experiment Folder Rules

## Parameter Input Contract
- All experiment runtime parameters MUST be loaded from a JSON config file.
- The command line MUST accept a JSON file path (for example: `./experiment_binary /path/to/config.json`).
- Experiments MUST NOT hardcode business parameters inside source code.
- If a required parameter is missing from the JSON file, the experiment MUST fail fast with a clear error message.
- For `experiment/basic_read_write`, use `experiment/Makefile` targets to drive start/stop/run flows.
- For `experiment/ycsb`, use `experiment/Makefile` targets: `ycsb-build`, `ycsb-load`, `ycsb-run`, `ycsb-all`.
- YCSB configs are scenario-based under `experiment/ycsb/scenarios/<scenario>/config.json`, selected by `YCSB_SCENARIO` (default `baseline`) or overridden by `YCSB_CONFIG`.
- Make targets can set infrastructure bootstrap knobs (for example image/container name), but experiment behavior parameters MUST still come from the JSON config.

## Infrastructure Workflow
- Current S3-compatible backend bootstrap for experiments is MinIO.
- Prefer `make -C experiment minio-s3-start`, `make -C experiment basic-read-write`, `make -C experiment minio-s3-stop`.
- For container cleanup, use `make -C experiment minio-s3-rm`.
- `experiment/scripts/minio_op.sh` uses podman.
- `stop` only stops the container by default; use `stop --rm` to remove it.

## Goal
- Keep experiment code reproducible and script-friendly.
