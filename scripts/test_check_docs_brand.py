"""Offline regression tests for documentation links and local skill metadata."""
from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location(
    "check_docs_brand", Path(__file__).with_name("check-docs-brand.py")
)
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


class DocumentLinksTests(unittest.TestCase):
    def test_inline_links_and_titles(self):
        self.assertEqual(CHECK.document_links('[a](a.md) ![b](b.svg "title")'), ['a.md', 'b.svg'])

    def test_html_entities(self):
        self.assertEqual(CHECK.document_links('<a href="a.md?x=1&amp;y=2">a</a>'), ['a.md?x=1&y=2'])

    def test_reference_definitions(self):
        self.assertEqual(CHECK.document_links('[guide][ref]\n\n[ref]: guide.md "Guide"'), ['guide.md'])

    def test_angle_destination(self):
        self.assertEqual(CHECK.document_links('[guide](<guide with spaces.md>)'), ['guide with spaces.md'])

    def test_parenthesized_filename(self):
        self.assertEqual(CHECK.document_links('[guide](guide(v2).md)'), ['guide(v2).md'])

    def test_backtick_fence(self):
        self.assertEqual(CHECK.document_links('```md\n[x](bad.md)\n```\n[x](good.md)'), ['good.md'])

    def test_tilde_fence(self):
        self.assertEqual(CHECK.document_links('~~~md\n[x](bad.md)\n~~~\n[x](good.md)'), ['good.md'])

    def test_nested_short_fence_does_not_close_example(self):
        self.assertEqual(CHECK.document_links('````md\n```\n[x](bad.md)\n```\n````\n[x](good.md)'), ['good.md'])

    def test_inline_code_is_not_a_link(self):
        self.assertEqual(CHECK.document_links('`[x](bad.md)` and [`file`](good.md)'), ['good.md'])


class RepositoryChecksTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def write(self, name, text=''):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding='utf-8')
        return path

    def skill(self, header='name: sample\ndescription: Use for a sample task.', body=''):
        return self.write('.agents/skills/sample/SKILL.md', f'---\n{header}\n---\n{body}')

    def test_relative_link_and_encoded_space(self):
        self.write('docs/a.md', '[b](../guide%20v2.md#part)')
        self.write('guide v2.md')
        self.assertEqual(CHECK.check_links(self.root, ('docs/a.md',)), [])

    def test_missing_target(self):
        self.write('a.md', '[b](missing.md)')
        self.assertIn('missing local target', CHECK.check_links(self.root, ('a.md',))[0])

    def test_missing_document_reports_instead_of_crashing(self):
        self.assertIn('cannot read document', CHECK.check_links(self.root, ('missing.md',))[0])

    def test_path_escape_rejected(self):
        self.write('a.md', '[x](../outside.md)')
        self.assertTrue(CHECK.check_links(self.root, ('a.md',)))

    def test_symlink_escape_rejected(self):
        with tempfile.TemporaryDirectory() as outside:
            target = Path(outside) / 'secret.md'
            target.write_text('not a repository document')
            (self.root / 'escape.md').symlink_to(target)
            self.write('a.md', '[x](escape.md)')
            self.assertTrue(CHECK.check_links(self.root, ('a.md',)))

    def test_external_urls_and_same_page_anchors(self):
        self.write('a.md', '[x](https://example.com/a) [m](mailto:help@example.com) [s](#section)')
        self.assertEqual(CHECK.check_links(self.root, ('a.md',)), [])

    def test_valid_skill(self):
        self.skill()
        self.assertEqual(CHECK.check_skills(self.root), [])

    def test_folded_description(self):
        self.skill('name: sample\ndescription: >-\n  Use when editing\n  a sample.')
        self.assertEqual(CHECK.check_skills(self.root), [])

    def test_quoted_fields(self):
        self.skill('name: "sample"\ndescription: \'Use for the sample.\'')
        self.assertEqual(CHECK.check_skills(self.root), [])

    def test_empty_description(self):
        self.skill('name: sample\ndescription: >-')
        self.assertIn('description', CHECK.check_skills(self.root)[0])

    def test_name_must_match_directory(self):
        self.skill('name: different\ndescription: Use for a sample.')
        self.assertIn('name must match', CHECK.check_skills(self.root)[0])

    def test_missing_frontmatter(self):
        self.write('.agents/skills/sample/SKILL.md', '# Sample')
        self.assertIn('frontmatter', CHECK.check_skills(self.root)[0])

    def test_legacy_web_paths_rejected(self):
        for body in ('cd web && bun run test', '`web/src/components`', '`web/scripts/add.mjs`', '`web/components.json`'):
            with self.subTest(body=body):
                self.skill(body=body)
                self.assertIn('stale frontend path', CHECK.check_skills(self.root)[0])

    def test_current_frontend_paths_accepted(self):
        self.skill(body='cd apps/web && bun run test\n`apps/web/src/components`')
        self.assertEqual(CHECK.check_skills(self.root), [])

    def test_vendored_skill_is_not_an_entry_point(self):
        self.skill()
        self.write('.agents/skills/sample/vendor/SKILL.md', 'upstream reference, not a local entry')
        self.assertEqual(len(CHECK.skill_files(self.root)), 1)
        self.assertEqual(CHECK.check_skills(self.root), [])

    def test_missing_skill_directory(self):
        self.assertIn('no project SKILL.md', CHECK.check_skills(self.root)[0])

    def test_skill_links_are_in_default_scope(self):
        self.skill(body='[broken](missing.md)')
        with patch.object(CHECK, 'DOCUMENTS', ()):
            self.assertIn('missing.md', CHECK.check_links(self.root)[0])

    def test_docs_only_does_not_call_asset_check(self):
        with patch.object(CHECK, 'check_links', return_value=[]), \
             patch.object(CHECK, 'check_skills', return_value=[]), \
             patch.object(CHECK, 'check_badges', return_value=[]), \
             patch.object(CHECK, 'check_assets') as assets:
            self.assertEqual(CHECK.main(['--docs-only']), 0)
            assets.assert_not_called()

    def test_default_keeps_asset_check(self):
        with patch.object(CHECK, 'check_links', return_value=[]), \
             patch.object(CHECK, 'check_skills', return_value=[]), \
             patch.object(CHECK, 'check_badges', return_value=[]), \
             patch.object(CHECK, 'check_assets', return_value=[]) as assets:
            self.assertEqual(CHECK.main([]), 0)
            assets.assert_called_once()


if __name__ == '__main__':
    unittest.main()
