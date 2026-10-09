/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { CLAUDE_OUTLINES, OPENAI_OUTLINES } from './brand-outlines'
import { Shape, rotate, tint, type Sculpture, type Vec3 } from './geometry'

type Outline = readonly (readonly [number, number])[]

/** Scan the actual mark contours once. No images, DOM, or network in the renderer. */
function mark(outlines: readonly Outline[], color: Vec3): Shape {
  const shape = new Shape()
  for (let row = 0; row < 150; row++) {
    const y = (row + 0.5) * 3.2
    const crossings: number[] = []
    for (const outline of outlines) {
      for (let i = 0; i < outline.length; i++) {
        const a = outline[i],
          b = outline[(i + 1) % outline.length]
        if ((a[1] > y) !== (b[1] > y)) {
          crossings.push(a[0] + ((y - a[1]) * (b[0] - a[0])) / (b[1] - a[1]))
        }
      }
    }
    crossings.sort((a, b) => a - b)
    for (let i = 0; i + 1 < crossings.length; i += 2) {
      for (
        let x = Math.ceil(crossings[i] / 3.2) * 3.2;
        x < crossings[i + 1];
        x += 3.2
      ) {
        const edge = Math.min(x - crossings[i], crossings[i + 1] - x)
        const z = 0.025 + Math.min(1, edge / 7) * 0.065
        for (const side of [-1, 1]) {
          shape.add(
            [(x - 240) / 218, (240 - y) / 218, z * side],
            tint(color, side < 0 ? 0.7 : 0.85 + z)
          )
        }
      }
    }
  }
  return shape
}

export function claudeMark(): Sculpture {
  return mark(CLAUDE_OUTLINES, [217, 119, 87]).model(
    (t) => {
      const sway = rotate(2, Math.sin(t * 1.15) * 0.14)
      return (p, v) => {
        sway(p, v)
        v[1] += Math.sin(t * 0.8) * 0.035
      }
    },
    [0.08, -0.08]
  )
}

export function openaiMark(): Sculpture {
  return mark(OPENAI_OUTLINES, [218, 231, 226]).model(
    (t) => rotate(2, t * 0.38),
    [0.16, -0.14]
  )
}
