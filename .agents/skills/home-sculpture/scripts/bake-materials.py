#!/usr/bin/env python3
# Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
"""Bake a checked subject mask into material indices, not an image or a 3D model.

Development dependency: Pillow. Input and mask must use the same pixel dimensions.
The output still needs sculpted depth, named moving parts and visual review.
"""
import argparse
from itertools import groupby
from pathlib import Path
import re

from PIL import Image

ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'


def encode(values):
    output = []
    for value, group in groupby(values):
        count = sum(1 for _ in group)
        while count:
            run = min(count, 64)
            output.append('~' + ALPHABET[run - 1] + ALPHABET[value]
                          if run >= 4 else ALPHABET[value] * run)
            count -= run
    return ''.join(output)


def bake(image_path, mask_path, width, height, colors, prefix):
    if not (8 <= width <= 512 and 8 <= height <= 512):
        raise ValueError('Grid width and height must be between 8 and 512')
    if not 2 <= colors <= 63:
        raise ValueError('Use 2 to 63 colors; index zero is empty space')
    if not re.fullmatch(r'[A-Z][A-Z0-9_]*', prefix):
        raise ValueError('Prefix must be an uppercase TypeScript identifier')
    with Image.open(image_path) as source, Image.open(mask_path) as mask:
        if source.size != mask.size:
            raise ValueError('Image and mask must have the same pixel dimensions')
        rgb = source.convert('RGB').resize((width, height), Image.Resampling.LANCZOS)
        alpha = mask.convert('L').resize((width, height), Image.Resampling.BOX)
    visible = [value > 230 for value in alpha.tobytes()]
    if not any(visible):
        raise ValueError('The mask contains no visible subject')
    quantized = rgb.quantize(colors=colors, method=Image.Quantize.MEDIANCUT)
    indices = [value + 1 if keep else 0
               for value, keep in zip(quantized.tobytes(), visible)]
    palette = quantized.getpalette()[:colors * 3]
    rows = [palette[i:i + 3] for i in range(0, len(palette), 3)]
    packed = encode(indices)
    lines = '\n'.join(f"  '{packed[i:i + 100]}'," for i in range(0, len(packed), 100))
    return (
        '/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */\n'
        '// Checked subject-only material indices. No runtime image request.\n'
        f'export const {prefix}_GRID = {{ width: {width}, height: {height} }} as const\n'
        f'export const {prefix}_COLORS = {rows!r} as const\n'
        f"export const {prefix}_MATERIALS = [\n{lines}\n].join('')\n"
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('image', type=Path)
    parser.add_argument('mask', type=Path, help='White subject, black background; review before baking')
    parser.add_argument('output', type=Path, help='New TypeScript file; existing files are never overwritten')
    parser.add_argument('--width', type=int, default=204)
    parser.add_argument('--height', type=int, default=196)
    parser.add_argument('--colors', type=int, default=40)
    parser.add_argument('--prefix', default='PORTRAIT')
    args = parser.parse_args()
    try:
        source = bake(args.image, args.mask, args.width, args.height, args.colors, args.prefix)
        # Exclusive creation protects the original reference and existing models.
        with args.output.open('x', encoding='utf-8') as output:
            output.write(source)
    except (OSError, ValueError) as error:
        parser.exit(1, f'Cannot bake materials: {error}\n')


if __name__ == '__main__':
    main()
