#!/usr/bin/env python3
import argparse
import csv
import json
import re
from pathlib import Path

SCENARIO_MAP = {
    "workloada": "ycsba",
    "workloadb": "ycsbb",
    "workloade": "ycsbe",
    "workloadf": "ycsbf",
}

TYPE_MAP = {
    "READ": "read",
    "UPDATE": "write",
    "INSERT": "insert",
    "SCAN": "scan",
    "READ_MODIFY_WRITE": "read-modify-write",
}

METRIC_RE = re.compile(
    r"^(READ|UPDATE|INSERT|SCAN|READ_MODIFY_WRITE)\s+- "
    r"Takes\(s\): [^,]+, Count: [^,]+, OPS: (?P<ops>[^,]+), .* "
    r"99\.99th\(us\): (?P<p9999>[0-9.]+)\s*$"
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Rebuild ycsb_summary.csv from an existing experiment result directory."
    )
    parser.add_argument("result_dir", help="Result directory like 20260305-003415")
    parser.add_argument(
        "--output",
        help="Output CSV path (default: <result_dir>/ycsb_summary.csv)",
    )
    parser.add_argument(
        "--system-name",
        default="Badger",
        help="System label to write into the CSV",
    )
    return parser.parse_args()


def load_jobs(summary_path: Path) -> list[dict]:
    payload = json.loads(summary_path.read_text(encoding="utf-8"))
    jobs = payload.get("jobs")
    if not isinstance(jobs, list):
        raise ValueError(f"{summary_path}: missing jobs array")
    return jobs


def parse_run_metrics(run_log: Path) -> dict[str, tuple[float, float]]:
    metrics: dict[str, tuple[float, float]] = {}
    for raw in run_log.read_text(encoding="utf-8", errors="ignore").splitlines():
        line = raw.strip()
        match = METRIC_RE.match(line)
        if not match:
            continue
        op = TYPE_MAP[match.group(1)]
        ops = float(match.group("ops"))
        p99_ms = float(match.group("p9999")) / 1000.0
        metrics[op] = (ops, p99_ms)
    return metrics


def collect_rows(result_dir: Path, system_name: str) -> list[dict[str, str]]:
    jobs = load_jobs(result_dir / "summary.json")
    rows: list[dict[str, str]] = []

    for job in jobs:
        scenario = SCENARIO_MAP.get(job["scenario"])
        if scenario is None:
            raise ValueError(f"unknown scenario: {job['scenario']}")

        run_dir = Path(job["run_dir"])
        if not run_dir.is_absolute():
            run_dir = result_dir / job["scenario"] / run_dir.name
        if not run_dir.exists():
            run_dir = result_dir / job["scenario"] / (
                f"value_{job['value_size']}__records_{job['recordcount']}"
                f"__ops_{job['operationcount']}__threads_{job['threadcount']}"
            )

        run_log = run_dir / "run.log"
        if not run_log.is_file():
            raise FileNotFoundError(f"missing run log: {run_log}")

        metrics = parse_run_metrics(run_log)
        for op_name, (ops, p99_ms) in sorted(metrics.items()):
            rows.append(
                {
                    "system": system_name,
                    "scenario": scenario,
                    "valuesize": f"{job['value_size']}b",
                    "type": op_name,
                    "ops": f"{ops:.3f}".rstrip("0").rstrip("."),
                    "p99latency(ms)": f"{p99_ms:.3f}".rstrip("0").rstrip("."),
                }
            )

    rows.sort(key=lambda row: (row["scenario"], int(row["valuesize"][:-1]), row["type"]))
    return rows


def write_csv(path: Path, rows: list[dict[str, str]]) -> None:
    fieldnames = ["system", "scenario", "valuesize", "type", "ops", "p99latency(ms)"]
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fh:
        writer = csv.DictWriter(fh, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def main() -> int:
    args = parse_args()
    result_dir = Path(args.result_dir).resolve()
    output = Path(args.output).resolve() if args.output else result_dir / "ycsb_summary.csv"
    rows = collect_rows(result_dir, args.system_name)
    write_csv(output, rows)
    print(output)
    print(f"rows: {len(rows)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
