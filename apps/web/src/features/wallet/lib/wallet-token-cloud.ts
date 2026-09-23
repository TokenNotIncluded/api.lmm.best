/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

export const MAX_BALANCE_PARTICLES = 1_200
export const MAX_PRESET_PARTICLES = 42
export const TOKEN_GLYPHS = [
  'token',
  '{}',
  '01',
  '[]',
  '</>',
  '::',
  '()',
  'ctx',
  '+',
  '/',
] as const

/** Stable seeded scatter: a growing balance reveals tokens without reshuffling old ones. */
export function createWalletTokenLayout(count = MAX_BALANCE_PARTICLES) {
  let seed = 0x7150c10d
  const random = () => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0
    return seed / 4294967296
  }
  const centers = [
    [-0.42, -0.05],
    [0.02, 0.12],
    [0.42, -0.1],
  ] as const
  return Array.from({ length: count }, () => {
    const center = centers[Math.floor(random() * centers.length)]
    const angle = random() * Math.PI * 2
    const radius = Math.sqrt(random()) * (0.74 + random() * 0.26)
    return {
      x: Math.max(-1, Math.min(1, center[0] + Math.cos(angle) * radius * 0.52)),
      y: Math.max(-1, Math.min(1, center[1] + Math.sin(angle) * radius * 0.75)),
      depth: random(),
      glyph: TOKEN_GLYPHS[Math.floor(random() * TOKEN_GLYPHS.length)],
    }
  })
}

/** Square-root density keeps small top-ups distinct without flattening large balances. */
export function walletCloudParticleCount(
  creditAmount: number,
  variant: 'balance' | 'preset'
) {
  if (!Number.isFinite(creditAmount) || creditAmount <= 0) return 0
  const count =
    variant === 'balance'
      ? Math.round(8 + 2.8 * Math.sqrt(creditAmount))
      : Math.round(4 + 0.85 * Math.sqrt(creditAmount))
  return Math.min(
    variant === 'balance' ? MAX_BALANCE_PARTICLES : MAX_PRESET_PARTICLES,
    Math.max(1, count)
  )
}

export function walletCloudExtent(
  creditAmount: number,
  variant: 'balance' | 'preset'
) {
  const amount = Number.isFinite(creditAmount) ? Math.max(0, creditAmount) : 0
  return variant === 'balance'
    ? 0.62 + 0.38 * (1 - Math.exp(-amount / 900))
    : 0.45 + 0.55 * (1 - Math.exp(-amount / 250))
}
