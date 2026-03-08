#!/usr/bin/env python3
import argparse
import csv
import random


def fmt_num(x):
    s = f"{x:.3f}"
    s = s.rstrip("0").rstrip(".")
    return s if s else "0"


def main():
    parser = argparse.ArgumentParser(
        description="Generate mock system data by jittering Badger rows"
    )
    parser.add_argument(
        "--input",
        default="experiment/ycsb/results/20260305-003415/ycsb_summary.csv",
        help="Input CSV path",
    )
    parser.add_argument(
        "--output",
        default="experiment/ycsb/results/20260305-003415/ycsb_summary_with_mock.csv",
        help="Output CSV path",
    )
    parser.add_argument(
        "--system-name",
        default="mock",
        help="System name for generated rows",
    )
    parser.add_argument(
        "--ops-jitter",
        type=float,
        default=0.15,
        help="Max relative jitter for OPS (e.g. 0.15 = ±15%%)",
    )
    parser.add_argument(
        "--p99-jitter",
        type=float,
        default=0.20,
        help="Max relative jitter for P99 latency (e.g. 0.20 = ±20%%)",
    )
    parser.add_argument(
        "--seed",
        type=int,
        default=42,
        help="Random seed",
    )
    parser.add_argument(
        "--only-mock",
        action="store_true",
        help="Write only mock rows (omit original rows)",
    )
    args = parser.parse_args()

    random.seed(args.seed)

    with open(args.input, newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        fieldnames = reader.fieldnames
        if not fieldnames:
            raise SystemExit("empty input CSV")
        rows = list(reader)

    mock_rows = []
    for row in rows:
        if row.get("system", "").lower() != "badger":
            continue
        try:
            ops = float(row["ops"])
            p99 = float(row["p99latency(ms)"])
        except (KeyError, ValueError):
            continue

        ops_factor = 1.0 + random.uniform(-args.ops_jitter, args.ops_jitter)
        p99_factor = 1.0 + random.uniform(-args.p99_jitter, args.p99_jitter)
        ops_new = max(0.001, ops * ops_factor)
        p99_new = max(0.001, p99 * p99_factor)

        new_row = dict(row)
        new_row["system"] = args.system_name
        new_row["ops"] = fmt_num(ops_new)
        new_row["p99latency(ms)"] = fmt_num(p99_new)
        mock_rows.append(new_row)

    out_rows = mock_rows if args.only_mock else rows + mock_rows

    with open(args.output, "w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(out_rows)

    print(args.output)
    print(f"original rows: {len(rows)}")
    print(f"mock rows: {len(mock_rows)}")


if __name__ == "__main__":
    main()
