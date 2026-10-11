"""Regression checks for the public, non-HTML agent policy."""
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class AgentPolicyTests(unittest.TestCase):
    def test_policy_is_real_markdown_not_an_empty_marker_or_html(self):
        policy = (ROOT / 'apps/web/public/AGENTS.md').read_text(encoding='utf-8')
        self.assertTrue(policy.startswith('# Agent rules'))
        self.assertIn('Do not register accounts in bulk.', policy)
        self.assertIn('only one account', policy)
        self.assertIn('每个用户只能', policy)
        self.assertNotIn('<html', policy.lower())

if __name__ == '__main__':
    unittest.main()
