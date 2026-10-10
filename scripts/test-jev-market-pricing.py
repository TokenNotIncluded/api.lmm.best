import importlib.util
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("jev_pricing", Path(__file__).with_name("jev-market-pricing.py"))
pricing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pricing)


class JevPricingTest(unittest.TestCase):
    def detail(self):
        return {"version": {"name": "Jev", "description": "Judgment tools", "endpoint": "https://mcp.example/jev", "visibility": "shared"},
                "allowed_users": [17], "tools": [{"name": "jev_verify", "description": "Verify claims", "input_schema": '{"type":"object"}', "output_schema": "", "permissions": '["network"]', "max_input_tokens": 65536}]}

    def test_10x_usd_price_uses_immutable_anchor_and_preserves_descriptions(self):
        detail = self.detail()
        draft = pricing.draft_for(detail, "3359744")
        self.assertEqual(draft["tools"][0]["input_token_price_quota"], 1411093)
        self.assertEqual(draft["tools"][0]["price_quota"], 92478)
        self.assertEqual(draft["description"], detail["version"]["description"])
        self.assertEqual(draft["tools"][0]["description"], detail["tools"][0]["description"])
        self.assertEqual(draft["allowed_users"], [17])
        self.assertEqual(pricing.draft_for(detail, "3500000")["tools"][0]["input_token_price_quota"], 1470000)

    def test_missing_anchor_and_non_jev_service_fail(self):
        for anchor in ["", "0", "-1", "NaN", "Infinity"]:
            with self.assertRaises((ValueError, pricing.decimal.InvalidOperation)):
                pricing.draft_for(self.detail(), anchor)
        detail = self.detail()
        detail["tools"][0]["name"] = "other_tool"
        with self.assertRaises(ValueError):
            pricing.draft_for(detail, "3500000")


if __name__ == "__main__":
    unittest.main()
