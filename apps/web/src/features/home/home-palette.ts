/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { PALETTE, type CorePalette, type Rgb } from './home-core'

const roles = {
  ground: '--home-stage-ground',
  wire: '--home-stage-wire',
  idle: '--home-stage-idle',
  lattice: '--home-stage-lattice',
  muted: '--home-stage-muted',
  forward: '--home-stage-forward',
  highlight: '--home-stage-highlight',
  feedback: '--home-stage-feedback',
  feedbackDeep: '--home-stage-feedback-deep',
  ink: '--home-stage-ink',
} as const

export function parseThemeColor(value: string): Rgb | null {
  const hex = value.trim().match(/^#([\da-f]{6})$/iu)
  if (hex) {
    return [0, 2, 4].map((offset) =>
      Number.parseInt(hex[1].slice(offset, offset + 2), 16)
    ) as unknown as Rgb
  }
  const rgb = value.trim().match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/iu)
  return rgb ? [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])] : null
}

export function homePalette(element: HTMLElement): CorePalette {
  const style = window.getComputedStyle(element)
  return Object.fromEntries(
    (Object.keys(roles) as (keyof CorePalette)[]).map((key) => [
      key,
      parseThemeColor(style.getPropertyValue(roles[key])) ?? PALETTE[key],
    ])
  ) as CorePalette
}

export function samePalette(a: CorePalette, b: CorePalette) {
  return (Object.keys(roles) as (keyof CorePalette)[]).every((key) =>
    a[key].every((value, index) => value === b[key][index])
  )
}
