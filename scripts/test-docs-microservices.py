"""Prevent retired deployment instructions and fixed policy copies returning."""
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[1]


class MicroserviceDocsTests(unittest.TestCase):
    def test_entry_guides_match_current_layout(self):
        for name in ("README.md", "README_EN.md", "docs/development.md", "docs/core-migration.md"):
            text = (ROOT / name).read_text()
            self.assertNotIn("apps/api-go", text)
            self.assertNotIn("apps/core-rust", text)
            self.assertNotIn("apps/extensions-go", text)
        for name in ("README.md", "README_EN.md", "docs/development.md"):
            self.assertIn("init-db", (ROOT / name).read_text())

    def test_old_install_guides_have_no_executable_steps(self):
        for name in ("manual-systemd-deployment.md", "postgresql-migration.md",
                     "postgresql-cutover.md", "production-release-transaction.md",
                     "seamless-upgrades.md", "backend-cli-deployment-contract.md"):
            text = (ROOT / "docs" / name).read_text()
            self.assertNotIn("```", text)
            self.assertIn("72667564c0431754d4856dc2e0db55f360bd2745", text)
            self.assertIn("release-architecture.md", text)

    def test_public_legal_aliases_do_not_publish_fixed_operator_details(self):
        for file, target in (("terms.html", "/user-agreement"), ("privacy.html", "/privacy-policy"),
                             ("safety-review.html", "/privacy-policy")):
            text = (ROOT / "apps/web/public/legal" / file).read_text()
            self.assertIn('href="' + target + '"', text)
            for fixed in ("api.lmm.best", "support@", "2026-", "non-refundable"):
                self.assertNotIn(fixed, text)

    def test_templates_are_files_not_compiled_policy_constants(self):
        code = (ROOT / "apps/lmm-extensions/internal/legal/legal.go").read_text()
        self.assertNotIn("go:embed", code)
        self.assertNotIn("api.lmm.best", code)
        for name in (ROOT / "config/legal/templates").rglob("*.md"):
            text = name.read_text()
            self.assertIn("{{operator_name}}", text)
            self.assertIn("{{effective_date}}", text)
            self.assertNotIn("support@", text)
            self.assertNotIn("api.lmm.best", text)
        config = json.loads((ROOT / "config/legal/site.example.json").read_text())
        self.assertIs(config["published"], False)

    def test_web_build_no_longer_reads_a_deleted_controller(self):
        scripts = json.loads((ROOT / "apps/web/package.json").read_text())["scripts"]
        self.assertNotIn("openui:check", scripts)
        self.assertFalse((ROOT / "apps/web/scripts/generate-assistant-openui.ts").exists())
        prompt_test = (ROOT / "apps/web/src/components/ai-elements/openui/library.test.tsx").read_text()
        self.assertNotIn("api-go/controller", prompt_test)
        self.assertIn("escapes data", prompt_test)


if __name__ == "__main__":
    unittest.main()
