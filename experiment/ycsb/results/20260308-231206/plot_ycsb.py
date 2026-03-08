#!/usr/bin/env python3
from pathlib import Path
import runpy
import sys


ROOT_SCRIPT = Path(__file__).resolve().parent.parent / "plot_ycsb.py"


if __name__ == "__main__":
    sys.argv = [str(ROOT_SCRIPT), str(Path(__file__).resolve().parent), *sys.argv[1:]]
    runpy.run_path(str(ROOT_SCRIPT), run_name="__main__")
