# Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
import importlib.util
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest

from PIL import Image

spec = importlib.util.spec_from_file_location('baker', Path(__file__).with_name('bake-materials.py'))
baker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(baker)


class BakeTests(unittest.TestCase):
    def test_runs_round_trip_across_64_cells(self):
        original = [0] * 300 + [1, 2, 3, 63] + [40] * 130
        encoded = baker.encode(original)
        decoded = []
        symbols = iter(encoded)
        for symbol in symbols:
            count = baker.ALPHABET.index(next(symbols)) + 1 if symbol == '~' else 1
            value = baker.ALPHABET.index(next(symbols) if symbol == '~' else symbol)
            decoded.extend([value] * count)
        self.assertEqual(decoded, original)

    def test_mask_dimensions_and_empty_mask(self):
        with TemporaryDirectory() as directory:
            image, mask = Path(directory) / 'image.png', Path(directory) / 'mask.png'
            Image.new('RGB', (16, 16)).save(image)
            Image.new('L', (8, 8)).save(mask)
            with self.assertRaisesRegex(ValueError, 'same pixel'):
                baker.bake(image, mask, 8, 8, 2, 'TEST')
            Image.new('L', (16, 16)).save(mask)
            with self.assertRaisesRegex(ValueError, 'no visible'):
                baker.bake(image, mask, 8, 8, 2, 'TEST')
            Image.new('L', (16, 16), 255).save(mask)
            result = baker.bake(image, mask, 8, 8, 2, 'TEST')
            self.assertIn('TEST_MATERIALS', result)
            self.assertEqual(result, baker.bake(image, mask, 8, 8, 2, 'TEST'))

    def test_invalid_budget_and_identifier(self):
        for width, height, colors, prefix in [(0, 8, 2, 'OK'), (8, 8, 64, 'OK'), (8, 8, 2, 'x;bad')]:
            with self.assertRaises(ValueError):
                baker.bake('missing', 'missing', width, height, colors, prefix)


if __name__ == '__main__':
    unittest.main()
