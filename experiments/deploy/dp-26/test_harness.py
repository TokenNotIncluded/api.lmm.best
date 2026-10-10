"""Integrity checks for reporting, not extra service-capacity tests."""
import copy
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location('summary', pathlib.Path(__file__).with_name('summarize.py'))
summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(summary)


def valid_record():
    return {'id': 'fixture-r1', 'source_slice': True, 'aborted': None,
            'results': [{'exit_code': 0, 'result': {
                'scope': 'source-slice fixture', 'offered': 10, 'success': 10,
                'failed': 0, 'rejected': 0, 'fixture_errors': 0,
                'inflight_at_end': 0, 'max_inflight': 4, 'mean_core_utilization': .2,
                'fixture_calls': 18, 'warmup_calls': 8}}]}


class IntegrityTests(unittest.TestCase):
    def test_good_record(self):
        summary.validate([valid_record()])

    def test_duplicate_run_is_not_another_repeat(self):
        with self.assertRaises(ValueError):
            summary.validate([valid_record(), valid_record()])

    def test_no_hidden_failure_or_leaked_work(self):
        for field in ['failed', 'rejected', 'fixture_errors', 'inflight_at_end']:
            record = valid_record()
            record['results'][0]['result'][field] = 1
            with self.subTest(field=field), self.assertRaises(ValueError):
                summary.validate([record])

    def test_unexpected_upstream_call_is_rejected(self):
        record = valid_record()
        record['results'][0]['result']['fixture_calls'] += 1
        with self.assertRaises(ValueError):
            summary.validate([record])

    def test_component_data_cannot_claim_full_system(self):
        record = valid_record()
        record['source_slice'] = False
        with self.assertRaises(ValueError):
            summary.validate([record])

    def test_outlier_is_retained_and_missing_is_not_zero(self):
        self.assertEqual(summary.stats([2, 3, 100])['values'], [2, 3, 100])
        self.assertEqual(summary.stats([2, 3, 100])['max'], 100)
        self.assertIsNone(summary.stats([None]))
        with self.assertRaises(ValueError):
            summary.stats([float('nan')])

if __name__ == '__main__':
    unittest.main()
