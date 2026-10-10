"""Regression checks for the public, non-HTML agent policy."""
import importlib.util
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location(
    'frontend_overlay', ROOT / 'scripts/render-frontend-proxy-overlay.py'
)
overlay = importlib.util.module_from_spec(spec)
spec.loader.exec_module(overlay)

ORIGINAL = b'''location @lmm_api_backend {
    proxy_pass http://127.0.0.1:3000;
}
location / {
    error_page 418 = @lmm_api_backend;
    return 418;
}
'''
ROUTES = """export interface FileRoutesByFullPath {
  '/': unknown
  '/store': unknown
  '/store/$id': unknown
}
"""


class AgentPolicyTests(unittest.TestCase):
    def test_policy_is_real_markdown_not_an_empty_marker_or_html(self):
        policy = (ROOT / 'apps/web/public/AGENTS.md').read_text(encoding='utf-8')
        self.assertTrue(policy.startswith('# Agent rules'))
        self.assertIn('Do not register accounts in bulk.', policy)
        self.assertIn('only one account', policy)
        self.assertIn('每个用户只能', policy)
        self.assertNotIn('<html', policy.lower())

    def test_agent_path_has_exact_plain_text_route_not_spa_fallback(self):
        result = overlay.render(ORIGINAL, ROUTES, ['index.html', 'AGENTS.md']).decode()
        agent = result.split('location = /AGENTS.md {\n', 1)[1].split('\n}\n', 1)[0]
        self.assertIn('types { }', agent)
        self.assertIn('default_type text/plain;', agent)
        self.assertIn('charset utf-8;', agent)
        self.assertIn('X-Content-Type-Options nosniff', agent)
        self.assertIn('try_files $uri =404;', agent)
        self.assertNotIn('/index.html', agent)
        self.assertIn('^(GET|HEAD)$', agent)
        self.assertIn('no-cache, must-revalidate', agent)
        self.assertIn('proxy_pass http://127.0.0.1:3000;', result)

    def test_normal_static_files_keep_their_mime_types(self):
        result = overlay.render(ORIGINAL, ROUTES, ['favicon.svg']).decode()
        icon = result.split('location = /favicon.svg {\n', 1)[1].split('\n}\n', 1)[0]
        self.assertNotIn('default_type text/plain', icon)

    def test_invalid_and_backend_names_are_still_rejected(self):
        for name in ['../AGENTS.md', 'api', 'scripts', 'bad;name.md']:
            with self.subTest(name=name), self.assertRaises(ValueError):
                overlay.render(ORIGINAL, ROUTES, [name])


if __name__ == '__main__':
    unittest.main()
