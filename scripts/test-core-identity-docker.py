#!/usr/bin/env python3
"""Identity persistence is exercised by the complete fresh-install RPC scenario."""
import runpy
from pathlib import Path

if __name__ == "__main__":
    runpy.run_path(str(Path(__file__).with_name("test-core-rpc-docker.py")), run_name="__main__")
