"""Regression tests for the offline documentation and logo checker."""
import importlib.util
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "docs_brand", Path(__file__).with_name("check-docs-brand.py")
)
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


class DocsBrandTests(unittest.TestCase):
    def test_links_ignore_code_and_keep_unicode_paths(self):
        self.assertEqual(
            CHECK.document_links('[说明](说明.md)\n```md\n[example](missing.md)\n```'),
            ['说明.md'],
        )

    def test_html_entities_are_decoded(self):
        self.assertEqual(
            CHECK.badge_urls('<img src="https://example.test/?a=1&amp;b=2">'),
            ['https://example.test/?a=1&b=2'],
        )

    def test_missing_link_is_reported(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in CHECK.DOCUMENTS:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('[missing](missing.md)')
            self.assertEqual(len(CHECK.check_links(root)), len(CHECK.DOCUMENTS))

    def test_old_symbol_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'mark.svg'
            path.write_text('<svg viewBox="0 0 128 128"><path d="old"/></svg>')
            self.assertTrue(any('differs' in error for error in CHECK.check_svg(path, 'new')))

    def test_scripts_and_external_entities_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'mark.svg'
            for text in (
                '<svg><script>alert(1)</script></svg>',
                '<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg/>',
                '<svg onload="alert(1)"/>',
            ):
                with self.subTest(text=text):
                    path.write_text(text)
                    self.assertTrue(CHECK.check_svg(path, 'new'))


if __name__ == '__main__':
    unittest.main()
