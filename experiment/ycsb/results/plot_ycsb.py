#!/usr/bin/env python3
import argparse
import csv
from collections import defaultdict
from pathlib import Path

import matplotlib.pyplot as plt
from matplotlib.legend_handler import HandlerTuple
from matplotlib.lines import Line2D
from matplotlib.patches import Patch


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Plot YCSB OPS and P99 latency figures from ycsb_summary.csv."
    )
    parser.add_argument(
        "result_dir",
        nargs="?",
        default=".",
        help="Result directory that contains ycsb_summary.csv",
    )
    parser.add_argument(
        "--csv",
        help="Input CSV path (default: <result_dir>/ycsb_summary.csv)",
    )
    parser.add_argument(
        "--out-dir",
        help="Output directory (default: result_dir)",
    )
    return parser.parse_args()


def load_data(csv_path: Path):
    data = defaultdict(lambda: defaultdict(lambda: defaultdict(list)))
    with csv_path.open(newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            system = row["system"]
            scenario = row["scenario"]
            typ = row["type"]
            valuesize = row["valuesize"]
            if not valuesize.endswith("b"):
                continue
            value = int(valuesize[:-1])
            ops = float(row["ops"])
            p99 = float(row["p99latency(ms)"])
            data[scenario][typ][system].append((value, ops, p99))

    for scenario in data:
        for typ in data[scenario]:
            for system in data[scenario][typ]:
                data[scenario][typ][system].sort(key=lambda x: x[0])
    return data


def format_value_label(value):
    if value >= 1024:
        v = value / 1024
        if v.is_integer():
            return f"{int(v)}K"
        return f"{v:.1f}K"
    return f"{value}B"


plt.rcParams["font.sans-serif"] = [
    "Noto Sans CJK SC",
    "Noto Sans CJK JP",
    "DejaVu Sans",
]
plt.rcParams["axes.unicode_minus"] = False

COLORS = [
    "#4C78A8",
    "#F58518",
    "#54A24B",
    "#E45756",
    "#72B7B2",
    "#EECA3B",
    "#B279A2",
    "#FF9DA6",
    "#9D755D",
    "#BAB0AC",
]
HATCHES = ["//", "..", "xx"]
MARKERS = ["o", "s", "^"]
LINESTYLES = ["-", "--", ":"]


def plot_ops_p99(ax, items_by_system, title, system_index):
    systems = sorted(items_by_system.keys(), key=lambda s: system_index[s])
    values = sorted({v for items in items_by_system.values() for v, _, _ in items})
    if not values:
        ax.set_title(f"{title} (missing)")
        ax.axis("off")
        return

    x = list(range(len(values)))
    labels = [format_value_label(v) for v in values]

    bar_width = 0.8 / max(len(systems), 1)
    offset_start = -0.4 + bar_width / 2
    ax2 = ax.twinx()

    for i, system in enumerate(systems):
        items = {v: (ops, p99) for v, ops, p99 in items_by_system[system]}
        ops = [items.get(v, (0.0, 0.0))[0] for v in values]
        p99 = [items.get(v, (0.0, 0.0))[1] for v in values]

        positions = [xi + offset_start + i * bar_width for xi in x]
        style_idx = system_index[system]
        hatch = HATCHES[style_idx % len(HATCHES)]
        marker = MARKERS[style_idx % len(MARKERS)]
        linestyle = LINESTYLES[style_idx % len(LINESTYLES)]
        color = COLORS[style_idx % len(COLORS)]

        ax.bar(
            positions,
            ops,
            width=bar_width,
            facecolor=color,
            edgecolor="black",
            hatch=hatch,
            linewidth=1.0,
            alpha=0.75,
        )
        ax2.plot(
            x,
            p99,
            color=color,
            marker=marker,
            linestyle=linestyle,
            linewidth=1.8,
            markerfacecolor="white",
            markeredgecolor=color,
        )

    ax.set_xticks(x)
    ax.set_xticklabels(labels)
    ax.set_xlabel("Value Size")
    ax.set_ylabel("OPS")
    ax.set_title(title)
    ax.set_ylim(bottom=0)
    ax2.set_ylabel("P99 Latency (ms)")
    ax2.set_ylim(bottom=0)


def plot_all(data, systems, system_index, out_dir: Path):
    fig = plt.figure(figsize=(13, 16), constrained_layout=True)
    gs = fig.add_gridspec(4, 2, height_ratios=[1.1, 1.1, 1.0, 1.0])

    ab_specs = [
        (gs[0, 0], "ycsba", "read", "YCSB-A Read"),
        (gs[0, 1], "ycsba", "write", "YCSB-A Write"),
        (gs[1, 0], "ycsbb", "read", "YCSB-B Read"),
        (gs[1, 1], "ycsbb", "write", "YCSB-B Write"),
    ]
    for cell, scenario, typ, title in ab_specs:
        ax = fig.add_subplot(cell)
        items = data.get(scenario, {}).get(typ, {})
        if not items:
            ax.set_title(f"{title} (missing)")
            ax.axis("off")
            continue
        plot_ops_p99(ax, items, title, system_index)

    e_specs = [
        (gs[2, 0], "scan", "YCSB-E Scan"),
        (gs[2, 1], "insert", "YCSB-E Insert"),
    ]
    for cell, typ, title in e_specs:
        ax = fig.add_subplot(cell)
        items = data.get("ycsbe", {}).get(typ, {})
        if not items:
            ax.set_title(f"{title} (missing)")
            ax.axis("off")
            continue
        plot_ops_p99(ax, items, title, system_index)

    ax_f = fig.add_subplot(gs[3, 0:2])
    items = data.get("ycsbf", {}).get("read-modify-write", {})
    if not items:
        ax_f.set_title("YCSB-F Read-Modify-Write (missing)")
        ax_f.axis("off")
    else:
        plot_ops_p99(ax_f, items, "YCSB-F Read-Modify-Write", system_index)

    handles = [
        Patch(facecolor="white", edgecolor="black"),
        Line2D([0], [0], color="black", marker="o", linestyle="-"),
    ]
    labels = ["OPS (bar)", "P99 Latency (line)"]

    system_handles = []
    for system in systems:
        idx = system_index[system]
        hatch = HATCHES[idx % len(HATCHES)]
        marker = MARKERS[idx % len(MARKERS)]
        linestyle = LINESTYLES[idx % len(LINESTYLES)]
        color = COLORS[idx % len(COLORS)]
        patch = Patch(facecolor=color, edgecolor="black", hatch=hatch)
        line = Line2D(
            [0],
            [0],
            color=color,
            marker=marker,
            linestyle=linestyle,
            markerfacecolor="white",
            markeredgecolor=color,
        )
        system_handles.append((patch, line))

    handles.extend(system_handles)
    labels.extend(systems)
    fig.legend(
        handles=handles,
        labels=labels,
        loc="lower center",
        bbox_to_anchor=(0.5, -0.04),
        ncol=len(handles),
        fontsize=9,
        handler_map={tuple: HandlerTuple(ndivide=None)},
    )

    png_path = out_dir / "ycsb_ops_p99_all.png"
    pdf_path = out_dir / "ycsb_ops_p99_all.pdf"
    fig.savefig(png_path, bbox_inches="tight")
    fig.savefig(pdf_path, bbox_inches="tight")
    return png_path, pdf_path


def main() -> int:
    args = parse_args()
    result_dir = Path(args.result_dir).resolve()
    csv_path = Path(args.csv).resolve() if args.csv else result_dir / "ycsb_summary.csv"
    out_dir = Path(args.out_dir).resolve() if args.out_dir else result_dir

    data = load_data(csv_path)
    systems = sorted(
        {
            system
            for scenario in data.values()
            for typ in scenario.values()
            for system in typ.keys()
        }
    )
    system_index = {system: i for i, system in enumerate(systems)}
    png_path, pdf_path = plot_all(data, systems, system_index, out_dir)
    print(png_path)
    print(pdf_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
