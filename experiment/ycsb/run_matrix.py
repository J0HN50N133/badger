#!/usr/bin/env python3
"""
Run a matrix of YCSB experiments against local Badger via experiment/ycsb wrapper.

Features:
1) choose a set of scenarios
2) set badger directory roots
3) force key length controls (default: 16B) and iterate value sizes
4) override YCSB parameters (recordcount, operationcount, threadcount, etc.)
5) write outputs grouped by scenario and parameter combination
"""

from __future__ import annotations

import argparse
import json
import os
import shlex
import subprocess
import sys
import time
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Dict, Iterable, List, Tuple

# ==================== User Tunables ====================
# Edit this block for your common experiment defaults.
#
# CLI flags still override these values, so you can keep one base profile
# here and only change a few parameters per run.
DEFAULTS = {
    # Scenario selection.
    "repo_root": ".",
    "scenario_root": "experiment/ycsb/scenarios",
    "scenarios": "all",  # e.g. "workloada,workloadb,workloadf"
    # Matrix knobs.
    "value_sizes": "128,256,512,1024",
    "recordcount": 1_000_000,
    "operationcount": 1_000_000,
    "threadcount": 16,
    # Badger storage roots.
    "badger_dir_root": "/tmp/badger-ycsb-matrix-lsm",
    # None means "same as badger_dir_root".
    "badger_value_dir_root": None,
    # Output.
    "output_dir": "experiment/ycsb/results",
    # build,load,run
    "phases": "build,load,run",
    # Key/value shape controls.
    "key_bytes": 16,
    "key_prefix": "",
    "field_count": 1,
    "badger_prefetch_size": None,
    "badger_value_log_gc_interval": None,
    "badger_value_log_gc_discard_ratio": None,
}


@dataclass
class Job:
    scenario: str
    value_size: int
    recordcount: str
    operationcount: str
    threadcount: str
    run_dir: Path
    config_path: Path
    workload_path: Path
    badger_dir: Path
    badger_value_dir: Path
    badger_prefetch_size: str | None
    badger_value_log_gc_interval: str | None
    badger_value_log_gc_discard_ratio: str | None


def job_to_json(job: Job) -> Dict[str, object]:
    out = asdict(job)
    for key, value in list(out.items()):
        if isinstance(value, Path):
            out[key] = str(value)
    return out


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run matrix YCSB experiments with scenario/value-size sweeps."
    )
    parser.add_argument(
        "--repo-root",
        default=DEFAULTS["repo_root"],
        help="Path to badger repository root (default: current directory).",
    )
    parser.add_argument(
        "--scenario-root",
        default=DEFAULTS["scenario_root"],
        help="Scenario root directory (default: experiment/ycsb/scenarios).",
    )
    parser.add_argument(
        "--scenarios",
        default=DEFAULTS["scenarios"],
        help="Comma-separated scenario names, or 'all' (default: all).",
    )
    parser.add_argument(
        "--value-sizes",
        default=DEFAULTS["value_sizes"],
        help="Comma-separated field length list in bytes, e.g. 128,256,512.",
    )
    parser.add_argument(
        "--recordcount",
        type=int,
        default=DEFAULTS["recordcount"],
        help="Override YCSB recordcount.",
    )
    parser.add_argument(
        "--operationcount",
        type=int,
        default=DEFAULTS["operationcount"],
        help="Override YCSB operationcount.",
    )
    parser.add_argument(
        "--threadcount",
        type=int,
        default=DEFAULTS["threadcount"],
        help="Override YCSB threadcount.",
    )
    parser.add_argument(
        "--ycsb-prop",
        action="append",
        default=[],
        help="Extra workload property override KEY=VALUE. Repeatable.",
    )
    parser.add_argument(
        "--load-prop",
        action="append",
        default=[],
        help="Load phase property override KEY=VALUE. Repeatable.",
    )
    parser.add_argument(
        "--run-prop",
        action="append",
        default=[],
        help="Run phase property override KEY=VALUE. Repeatable.",
    )
    parser.add_argument(
        "--badger-prop",
        action="append",
        default=[],
        help="Badger extra property override KEY=VALUE into extraProperties. Repeatable.",
    )
    parser.add_argument(
        "--badger-prefetch-size",
        type=int,
        default=DEFAULTS["badger_prefetch_size"],
        help=(
            "Set badger.scan_prefetch_size for SCAN iterator prefetching "
            "(default: keep scenario/default setting)."
        ),
    )
    parser.add_argument(
        "--badger-value-log-gc-interval",
        default=DEFAULTS["badger_value_log_gc_interval"],
        help=(
            "Set badger.value_log_gc_interval, for example 0s, 30s, or 5m "
            "(default: keep scenario/default setting)."
        ),
    )
    parser.add_argument(
        "--badger-value-log-gc-discard-ratio",
        type=float,
        default=DEFAULTS["badger_value_log_gc_discard_ratio"],
        help=(
            "Set badger.value_log_gc_discard_ratio in (0, 1) "
            "(default: keep scenario/default setting)."
        ),
    )
    parser.add_argument(
        "--badger-dir-root",
        default=DEFAULTS["badger_dir_root"],
        help="Root directory for badger.dir per job.",
    )
    parser.add_argument(
        "--badger-value-dir-root",
        default=DEFAULTS["badger_value_dir_root"],
        help="Root directory for badger.valuedir per job (default: same as --badger-dir-root).",
    )
    parser.add_argument(
        "--output-dir",
        default=DEFAULTS["output_dir"],
        help="Output root for generated configs/logs/results.",
    )
    parser.add_argument(
        "--run-id",
        default=time.strftime("%Y%m%d-%H%M%S"),
        help="Run identifier under output-dir (default: timestamp).",
    )
    parser.add_argument(
        "--phases",
        default=DEFAULTS["phases"],
        help="Comma-separated phases among: build,load,run (default: build,load,run).",
    )
    parser.add_argument(
        "--continue-on-error",
        action="store_true",
        help="Continue running remaining jobs when one job fails.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Only generate configs/workloads and print commands.",
    )

    # Key/value shape controls.
    parser.add_argument(
        "--key-bytes",
        type=int,
        default=DEFAULTS["key_bytes"],
        help="Target key length in bytes (default: 16).",
    )
    parser.add_argument(
        "--key-prefix",
        default=DEFAULTS["key_prefix"],
        help="Key prefix used with zeropadding to target key length (default: empty).",
    )
    parser.add_argument(
        "--field-count",
        type=int,
        default=DEFAULTS["field_count"],
        help="Field count per record (default: 1).",
    )
    return parser.parse_args()


def parse_csv(raw: str) -> List[str]:
    return [x.strip() for x in raw.split(",") if x.strip()]


def parse_kv_list(entries: Iterable[str]) -> Dict[str, str]:
    out: Dict[str, str] = {}
    for entry in entries:
        if "=" not in entry:
            raise ValueError(f"invalid KEY=VALUE pair: {entry!r}")
        key, value = entry.split("=", 1)
        key = key.strip()
        if not key:
            raise ValueError(f"empty key in pair: {entry!r}")
        out[key] = value.strip()
    return out


def load_json(path: Path) -> Dict[str, object]:
    return json.loads(path.read_text(encoding="utf-8"))


def save_json(path: Path, data: Dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def load_properties(path: Path) -> Dict[str, str]:
    props: Dict[str, str] = {}
    for raw in path.read_text(encoding="utf-8", errors="ignore").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            continue
        key, value = line.split("=", 1)
        props[key.strip()] = value.strip()
    return props


def save_properties(path: Path, props: Dict[str, str], header: List[str] | None = None) -> None:
    lines: List[str] = []
    if header:
        lines.extend([f"# {h}" for h in header])
        lines.append("")
    for key in sorted(props.keys()):
        lines.append(f"{key}={props[key]}")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def discover_scenarios(scenario_root: Path) -> List[str]:
    names: List[str] = []
    for child in sorted(scenario_root.iterdir()):
        if not child.is_dir():
            continue
        if (child / "config.json").is_file() and (child / "workload").is_file():
            names.append(child.name)
    return names


def run_cmd(
    cmd: List[str],
    cwd: Path,
    log_path: Path,
    dry_run: bool = False,
) -> Tuple[int, float]:
    rendered = " ".join(shlex.quote(x) for x in cmd)
    print(f"[exec] {rendered}")
    if dry_run:
        return 0, 0.0

    log_path.parent.mkdir(parents=True, exist_ok=True)
    start = time.time()
    with log_path.open("w", encoding="utf-8") as logf:
        proc = subprocess.Popen(
            cmd,
            cwd=str(cwd),
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        assert proc.stdout is not None
        for line in proc.stdout:
            sys.stdout.write(line)
            logf.write(line)
        rc = proc.wait()
    return rc, time.time() - start


def main() -> int:
    args = parse_args()

    repo_root = Path(args.repo_root).resolve()
    scenario_root = (repo_root / args.scenario_root).resolve()
    output_root = (repo_root / args.output_dir).resolve() / args.run_id
    badger_dir_root = Path(args.badger_dir_root).resolve()
    badger_value_dir_root = (
        Path(args.badger_value_dir_root).resolve()
        if args.badger_value_dir_root
        else badger_dir_root
    )

    phases = parse_csv(args.phases)
    valid_phases = {"build", "load", "run"}
    if not phases or any(p not in valid_phases for p in phases):
        raise ValueError(f"--phases must be subset of {sorted(valid_phases)}")

    value_sizes = [int(x) for x in parse_csv(args.value_sizes)]
    if any(v <= 0 for v in value_sizes):
        raise ValueError("all --value-sizes must be > 0")

    if args.field_count <= 0:
        raise ValueError("--field-count must be > 0")

    if args.key_bytes <= 0:
        raise ValueError("--key-bytes must be > 0")

    if args.badger_prefetch_size is not None and args.badger_prefetch_size <= 0:
        raise ValueError("--badger-prefetch-size must be > 0")

    if (
        args.badger_value_log_gc_interval is not None
        and not str(args.badger_value_log_gc_interval).strip()
    ):
        raise ValueError("--badger-value-log-gc-interval must not be empty")

    if (
        args.badger_value_log_gc_discard_ratio is not None
        and not 0 < args.badger_value_log_gc_discard_ratio < 1
    ):
        raise ValueError("--badger-value-log-gc-discard-ratio must be in (0, 1)")

    if len(args.key_prefix.encode("utf-8")) >= args.key_bytes:
        raise ValueError("--key-prefix byte length must be less than --key-bytes")

    all_scenarios = discover_scenarios(scenario_root)
    if not all_scenarios:
        raise ValueError(f"no scenarios found under {scenario_root}")

    if args.scenarios.strip().lower() == "all":
        scenarios = all_scenarios
    else:
        requested = parse_csv(args.scenarios)
        missing = [s for s in requested if s not in all_scenarios]
        if missing:
            raise ValueError(f"unknown scenarios: {missing}; available: {all_scenarios}")
        scenarios = requested

    ycsb_overrides = parse_kv_list(args.ycsb_prop)
    load_overrides = parse_kv_list(args.load_prop)
    run_overrides = parse_kv_list(args.run_prop)
    badger_overrides = parse_kv_list(args.badger_prop)

    jobs: List[Job] = []
    for scenario in scenarios:
        scenario_dir = scenario_root / scenario
        cfg_path = scenario_dir / "config.json"
        workload_path = scenario_dir / "workload"

        base_cfg = load_json(cfg_path)
        base_workload = load_properties(workload_path)

        for value_size in value_sizes:
            props = dict(base_workload)

            # Fixed key shape: keyprefix + zero-padded decimal, with ordered inserts.
            zero_padding = args.key_bytes - len(args.key_prefix.encode("utf-8"))
            props["keyprefix"] = args.key_prefix
            props["zeropadding"] = str(zero_padding)
            props["insertorder"] = "ordered"
            props["requestdistribution"] = "zipfian"

            # Value size control.
            props["fieldcount"] = str(args.field_count)
            props["fieldlengthdistribution"] = "constant"
            props["fieldlength"] = str(value_size)

            # Optional YCSB overrides.
            if args.recordcount is not None:
                props["recordcount"] = str(args.recordcount)
            if args.operationcount is not None:
                props["operationcount"] = str(args.operationcount)
            if args.threadcount is not None:
                props["threadcount"] = str(args.threadcount)
            props.update(ycsb_overrides)

            recordcount = props.get("recordcount", "na")
            operationcount = props.get("operationcount", "na")
            threadcount = props.get("threadcount", "na")

            run_key = (
                f"value_{value_size}__records_{recordcount}"
                f"__ops_{operationcount}__threads_{threadcount}"
            )
            run_dir = output_root / scenario / run_key
            generated_workload = run_dir / "workload.generated"
            generated_config = run_dir / "config.generated.json"

            save_properties(
                generated_workload,
                props,
                header=[
                    f"Generated from scenario {scenario}",
                    f"value_size={value_size}",
                ],
            )

            job_cfg = dict(base_cfg)
            job_cfg["workloadFile"] = str(generated_workload)
            job_cfg["badgerDir"] = str(badger_dir_root / args.run_id / scenario / run_key)
            job_cfg["badgerValueDir"] = str(
                badger_value_dir_root / args.run_id / scenario / run_key
            )
            job_cfg.setdefault("extraProperties", {})
            if not isinstance(job_cfg["extraProperties"], dict):
                raise ValueError(f"{cfg_path}: extraProperties must be object")
            job_cfg["extraProperties"].update(badger_overrides)
            if args.badger_prefetch_size is not None:
                job_cfg["extraProperties"]["badger.scan_prefetch_size"] = str(
                    args.badger_prefetch_size
                )
            if args.badger_value_log_gc_interval is not None:
                job_cfg["extraProperties"]["badger.value_log_gc_interval"] = str(
                    args.badger_value_log_gc_interval
                )
            if args.badger_value_log_gc_discard_ratio is not None:
                job_cfg["extraProperties"]["badger.value_log_gc_discard_ratio"] = str(
                    args.badger_value_log_gc_discard_ratio
                )

            job_cfg.setdefault("loadProperties", {})
            if not isinstance(job_cfg["loadProperties"], dict):
                raise ValueError(f"{cfg_path}: loadProperties must be object")
            job_cfg["loadProperties"].update(load_overrides)

            job_cfg.setdefault("runProperties", {})
            if not isinstance(job_cfg["runProperties"], dict):
                raise ValueError(f"{cfg_path}: runProperties must be object")
            job_cfg["runProperties"].update(run_overrides)

            save_json(generated_config, job_cfg)

            meta = {
                "scenario": scenario,
                "value_size": value_size,
                "recordcount": recordcount,
                "operationcount": operationcount,
                "threadcount": threadcount,
                "badger_prefetch_size": job_cfg["extraProperties"].get(
                    "badger.scan_prefetch_size"
                ),
                "badger_value_log_gc_interval": job_cfg["extraProperties"].get(
                    "badger.value_log_gc_interval"
                ),
                "badger_value_log_gc_discard_ratio": job_cfg["extraProperties"].get(
                    "badger.value_log_gc_discard_ratio"
                ),
                "generated_at": time.strftime("%Y-%m-%d %H:%M:%S"),
                "workload_file": str(generated_workload),
                "config_file": str(generated_config),
            }
            save_json(run_dir / "job.meta.json", meta)

            jobs.append(
                Job(
                    scenario=scenario,
                    value_size=value_size,
                    recordcount=str(recordcount),
                    operationcount=str(operationcount),
                    threadcount=str(threadcount),
                    run_dir=run_dir,
                    config_path=generated_config,
                    workload_path=generated_workload,
                    badger_dir=Path(job_cfg["badgerDir"]),
                    badger_value_dir=Path(job_cfg["badgerValueDir"]),
                    badger_prefetch_size=job_cfg["extraProperties"].get(
                        "badger.scan_prefetch_size"
                    ),
                    badger_value_log_gc_interval=job_cfg["extraProperties"].get(
                        "badger.value_log_gc_interval"
                    ),
                    badger_value_log_gc_discard_ratio=job_cfg["extraProperties"].get(
                        "badger.value_log_gc_discard_ratio"
                    ),
                )
            )

    if not jobs:
        raise ValueError("no jobs generated")

    summary: List[Dict[str, object]] = []
    failed = False

    # Build once, then run load/run per job.
    if "build" in phases:
        first = jobs[0]
        rc, sec = run_cmd(
            ["go", "run", "./experiment/ycsb", str(first.config_path), "build"],
            cwd=repo_root,
            log_path=output_root / "build.log",
            dry_run=args.dry_run,
        )
        summary.append(
            {
                "type": "build",
                "config": str(first.config_path),
                "returncode": rc,
                "duration_seconds": sec,
            }
        )
        if rc != 0:
            failed = True
            if not args.continue_on_error:
                save_json(
                    output_root / "summary.json",
                    {"jobs": [job_to_json(j) for j in jobs], "runs": summary},
                )
                return rc

    for idx, job in enumerate(jobs, start=1):
        print(
            f"\n=== [{idx}/{len(jobs)}] scenario={job.scenario} "
            f"value={job.value_size} recordcount={job.recordcount} "
            f"operationcount={job.operationcount} threadcount={job.threadcount} "
            f"badger_prefetch_size={job.badger_prefetch_size or 'default'} "
            f"badger_gc_interval={job.badger_value_log_gc_interval or 'default'} "
            f"badger_gc_discard_ratio="
            f"{job.badger_value_log_gc_discard_ratio or 'default'} ==="
        )

        for phase in ["load", "run"]:
            if phase not in phases:
                continue
            log_path = job.run_dir / f"{phase}.log"
            rc, sec = run_cmd(
                ["go", "run", "./experiment/ycsb", str(job.config_path), phase],
                cwd=repo_root,
                log_path=log_path,
                dry_run=args.dry_run,
            )
            summary.append(
                {
                    "type": phase,
                    "scenario": job.scenario,
                    "value_size": job.value_size,
                    "recordcount": job.recordcount,
                    "operationcount": job.operationcount,
                    "threadcount": job.threadcount,
                    "badger_prefetch_size": job.badger_prefetch_size,
                    "badger_value_log_gc_interval": job.badger_value_log_gc_interval,
                    "badger_value_log_gc_discard_ratio": (
                        job.badger_value_log_gc_discard_ratio
                    ),
                    "run_dir": str(job.run_dir),
                    "config": str(job.config_path),
                    "workload": str(job.workload_path),
                    "returncode": rc,
                    "duration_seconds": sec,
                    "log": str(log_path),
                }
            )
            if rc != 0:
                failed = True
                if not args.continue_on_error:
                    save_json(
                        output_root / "summary.json",
                        {"jobs": [job_to_json(j) for j in jobs], "runs": summary},
                    )
                    return rc

    save_json(
        output_root / "summary.json",
        {"jobs": [job_to_json(j) for j in jobs], "runs": summary},
    )
    print(f"\nSummary written to: {output_root / 'summary.json'}")
    return 1 if failed else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130)
