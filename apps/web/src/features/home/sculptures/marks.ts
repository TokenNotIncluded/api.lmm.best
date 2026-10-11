/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  DEEPSEEK_OUTLINES,
  GEMINI_OUTLINES,
  GLM_OUTLINES,
  GROK_OUTLINES,
  KIMI_OUTLINES,
  MISTRAL_OUTLINES,
  OLLAMA_OUTLINES,
  QWEN_OUTLINES,
} from './ai-brand-outlines'
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
        if (a[1] > y !== b[1] > y) {
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

/** Small 3D turns show the thickness without turning word-like marks upside down. */
function floatingMark(outlines: readonly Outline[], color: Vec3): Sculpture {
  return mark(outlines, color).model(
    (t) => {
      const yaw = rotate(1, Math.sin(t * 0.65) * 0.22)
      const roll = rotate(2, Math.sin(t * 0.45) * 0.055)
      return (p, v) => {
        yaw(p, v)
        roll(p, v)
        v[1] += Math.sin(t * 0.8) * 0.035
      }
    },
    [0.08, -0.08]
  )
}

export const grokMark = (): Sculpture =>
  floatingMark(GROK_OUTLINES, [226, 231, 237])
export const glmMark = (): Sculpture =>
  floatingMark(GLM_OUTLINES, [104, 143, 245])
export const kimiMark = (): Sculpture =>
  floatingMark(KIMI_OUTLINES, [190, 228, 210])
export const deepseekMark = (): Sculpture =>
  floatingMark(DEEPSEEK_OUTLINES, [92, 139, 243])
export const qwenMark = (): Sculpture =>
  floatingMark(QWEN_OUTLINES, [164, 139, 238])
export const geminiMark = (): Sculpture =>
  floatingMark(GEMINI_OUTLINES, [114, 160, 244])
export const mistralMark = (): Sculpture =>
  floatingMark(MISTRAL_OUTLINES, [247, 157, 79])
export const ollamaMark = (): Sculpture =>
  floatingMark(OLLAMA_OUTLINES, [230, 230, 224])
