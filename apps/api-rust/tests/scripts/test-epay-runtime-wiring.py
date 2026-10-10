#!/usr/bin/env python3
"""Ensure disabled adapter and missing cache/root composition cannot earn credit."""

import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("epay_wiring", Path(__file__).with_name("check-epay-runtime-wiring.py"))
wiring = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wiring)


class EpayWiringTests(unittest.TestCase):
    def test_runtime_regressions_are_rejected(self):
        source = (wiring.ROOT / "apps/api-rust/src/main.rs").read_text()
        wiring.check_source(source)
        for broken in (
            source.replace("PgEpayGateway::new", "DisabledEpayGateway::new"),
            source.replace("PgEpayRepository::new", "DisabledTopupRepository::new"),
            source.replace(".with_valkey(valkey.clone())", ""),
            source.replace(".merge(epay)", ".merge(other_router)"),
        ):
            with self.assertRaises(ValueError):
                wiring.check_source(broken)


if __name__ == "__main__":
    unittest.main()
