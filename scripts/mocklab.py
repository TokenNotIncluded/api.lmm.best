#!/usr/bin/env python3
"""Repository entry for the optional test provider lab. Does not deploy anything."""
from pathlib import Path
import signal
import sys

# Import only repository test code, never application runtime modules.
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "tests"))
from mocklab.cli import main


def stop(_signal, _frame):
    raise KeyboardInterrupt


if __name__ == "__main__":
    if sys.flags.optimize:
        raise SystemExit("mocklab requires assertions: do not use python -O or PYTHONOPTIMIZE")
    signal.signal(signal.SIGTERM, stop)
    raise SystemExit(main())
