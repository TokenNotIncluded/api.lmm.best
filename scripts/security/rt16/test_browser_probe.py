# Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
"""Runner input validation only; no browser or application is tested here."""
import json
from pathlib import Path
import tempfile
import unittest

from browser_probe import CONTROLS, load_cases


class CorpusValidation(unittest.TestCase):
    def load(self, value):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "corpus.json"
            path.write_text(json.dumps(value), encoding="utf-8")
            return load_cases(path)

    def test_absent_corpus_is_not_an_attack_test(self):
        self.assertEqual(load_cases(None), [])

    def test_legal_control_required(self):
        for name in CONTROLS:
            case = {"id": name, "markdown": "plain text", "control": name}
            self.assertEqual(self.load([case]), [case])
        with self.assertRaises(ValueError):
            self.load([{"id": "unpaired", "markdown": "text"}])

    def test_invalid_ids_and_inputs_fail_closed(self):
        case = {"id": "one", "markdown": "text", "control": "html"}
        for invalid in [{}, [], [case] * 101, [case, case],
                        [dict(case, id="")], [dict(case, id="x" * 81)],
                        [dict(case, id=7)], [dict(case, markdown=7)],
                        [dict(case, markdown="x" * 50001)],
                        [dict(case, control="unknown")], [dict(case, control=[])],
                        [dict(case, control={})], [None]]:
            with self.subTest(value=type(invalid).__name__):
                with self.assertRaises(ValueError):
                    self.load(invalid)

    def test_boundary_lengths_are_accepted(self):
        cases = [{"id": str(index).zfill(80), "markdown": "x" * 50000,
                  "control": "html"} for index in range(100)]
        self.assertEqual(self.load(cases), cases)


if __name__ == "__main__":
    unittest.main()
