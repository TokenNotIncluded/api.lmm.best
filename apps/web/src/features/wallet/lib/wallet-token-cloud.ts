/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

export const MAX_BALANCE_PARTICLES = 1_200
export const MAX_PRESET_PARTICLES = 42

type RandomSource = () => number

export type WalletTokenPoint = {
  x: number
  y: number
  depth: number
  glyph: string
}

function createSeededRandom(seed: number): RandomSource {
  let state = (Number.isFinite(seed) ? seed : 0) >>> 0
  if (state === 0) state = 0x9e3779b9
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0
    return state / 4294967296
  }
}

function randomInt(random: RandomSource, maxExclusive: number) {
  return Math.floor(random() * maxExclusive)
}

function randomLetter(random: RandomSource) {
  return String.fromCharCode(97 + randomInt(random, 26))
}

function randomDigit(random: RandomSource) {
  return String.fromCharCode(48 + randomInt(random, 10))
}

function randomPunctuation(random: RandomSource) {
  const bucket = randomInt(random, 4)
  const start = bucket === 0 ? 33 : bucket === 1 ? 58 : bucket === 2 ? 91 : 123
  const length = bucket === 0 ? 15 : bucket === 1 ? 7 : bucket === 2 ? 6 : 4
  let value = String.fromCharCode(start + randomInt(random, length))
  // Decorative wallet tokens must not resemble a settlement currency symbol.
  while (value.charCodeAt(0) === 36) {
    value = String.fromCharCode(start + randomInt(random, length))
  }
  return value
}
function randomSequence(
  random: RandomSource,
  length: number,
  next: (random: RandomSource) => string
) {
  let value = ''
  for (let index = 0; index < length; index += 1) value += next(random)
  return value
}

/**
 * Build short code-like fragments procedurally instead of selecting from a
 * fixed vocabulary. This keeps each cloud visually distinct without shipping
 * a repeated hard-coded token list.
 */
function createWalletTokenGlyph(random: RandomSource) {
  const kind = randomInt(random, 6)
  if (kind === 0) {
    return randomSequence(random, 2 + randomInt(random, 4), randomLetter)
  }
  if (kind === 1) {
    return `${randomLetter(random)}${randomSequence(
      random,
      1 + randomInt(random, 3),
      randomDigit
    )}`
  }
  if (kind === 2) {
    return randomSequence(random, 1 + randomInt(random, 3), randomDigit)
  }
  if (kind === 3) {
    return randomSequence(random, 1 + randomInt(random, 2), randomPunctuation)
  }
  if (kind === 4) {
    return `${randomPunctuation(random)}${randomSequence(
      random,
      1 + randomInt(random, 3),
      randomLetter
    )}`
  }
  return `${randomSequence(
    random,
    1 + randomInt(random, 3),
    randomLetter
  )}${randomPunctuation(random)}`
}

/**
 * A fresh seed per mounted cloud makes separate cards and page visits look
 * different. Layout generation stays seeded so rerenders do not reshuffle.
 */
export function createWalletTokenSeed() {
  const values = new Uint32Array(1)
  const cryptoSource = globalThis.crypto
  if (cryptoSource?.getRandomValues) {
    cryptoSource.getRandomValues(values)
    if (values[0] !== 0) return values[0]
  }
  const fallback =
    ((Date.now() >>> 0) ^ Math.floor(Math.random() * 4294967296)) >>> 0
  return fallback || 0x9e3779b9
}

/**
 * Seeded procedural scatter. Lobe placement, glyphs and particle placement all
 * come from the supplied seed, while a growing balance still reveals existing
 * particles in the same order instead of scrambling the cloud.
 */
export function createWalletTokenLayout(
  count = MAX_BALANCE_PARTICLES,
  seed = createWalletTokenSeed()
): WalletTokenPoint[] {
  const random = createSeededRandom(seed)
  const safeCount = Math.max(0, Math.floor(Number.isFinite(count) ? count : 0))
  const lobeCount = 2 + randomInt(random, 4)
  const phase = random() * Math.PI * 2
  const centers = Array.from({ length: lobeCount }, (_, index) => {
    const angle =
      phase +
      (index / lobeCount) * Math.PI * 2 +
      (random() - 0.5) * (Math.PI / lobeCount)
    const radius = 0.08 + random() * 0.28
    return {
      x: Math.cos(angle) * radius,
      y: Math.sin(angle) * radius * (0.45 + random() * 0.25),
      spreadX: 0.34 + random() * 0.24,
      spreadY: 0.42 + random() * 0.3,
    }
  })

  return Array.from({ length: safeCount }, () => {
    const center = centers[randomInt(random, centers.length)]
    const angle = random() * Math.PI * 2
    const radius = Math.sqrt(random())
    const jitterX = (random() - 0.5) * 0.1
    const jitterY = (random() - 0.5) * 0.12
    const depth = Math.max(
      0,
      Math.min(1, 0.22 + (1 - radius * 0.58) * 0.72 + (random() - 0.5) * 0.24)
    )

    return {
      x: Math.max(
        -1,
        Math.min(
          1,
          center.x + Math.cos(angle) * radius * center.spreadX + jitterX
        )
      ),
      y: Math.max(
        -1,
        Math.min(
          1,
          center.y + Math.sin(angle) * radius * center.spreadY + jitterY
        )
      ),
      depth,
      glyph: createWalletTokenGlyph(random),
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
) {
    value = String.fromCharCode(start + randomInt(random, length))
  }
  return value
}

function randomSequence(
  random: RandomSource,
  length: number,
  next: (random: RandomSource) => string
) {
  let value = ''
  for (let index = 0; index < length; index += 1) value += next(random)
  return value
}

/**
 * Build short code-like fragments procedurally instead of selecting from a
 * fixed vocabulary. This keeps each cloud visually distinct without shipping
 * a repeated hard-coded token list.
 */
function createWalletTokenGlyph(random: RandomSource) {
  const kind = randomInt(random, 6)
  if (kind === 0) {
    return randomSequence(random, 2 + randomInt(random, 4), randomLetter)
  }
  if (kind === 1) {
    return `${randomLetter(random)}${randomSequence(
      random,
      1 + randomInt(random, 3),
      randomDigit
    )}`
  }
  if (kind === 2) {
    return randomSequence(random, 1 + randomInt(random, 3), randomDigit)
  }
  if (kind === 3) {
    return randomSequence(random, 1 + randomInt(random, 2), randomPunctuation)
  }
  if (kind === 4) {
    return `${randomPunctuation(random)}${randomSequence(
      random,
      1 + randomInt(random, 3),
      randomLetter
    )}`
  }
  return `${randomSequence(
    random,
    1 + randomInt(random, 3),
    randomLetter
  )}${randomPunctuation(random)}`
}

/**
 * A fresh seed per mounted cloud makes separate cards and page visits look
 * different. Layout generation stays seeded so rerenders do not reshuffle.
 */
export function createWalletTokenSeed() {
  const values = new Uint32Array(1)
  const cryptoSource = globalThis.crypto
  if (cryptoSource?.getRandomValues) {
    cryptoSource.getRandomValues(values)
    if (values[0] !== 0) return values[0]
  }
  const fallback =
    ((Date.now() >>> 0) ^ Math.floor(Math.random() * 4294967296)) >>> 0
  return fallback || 0x9e3779b9
}

/**
 * Seeded procedural scatter. Lobe placement, glyphs and particle placement all
 * come from the supplied seed, while a growing balance still reveals existing
 * particles in the same order instead of scrambling the cloud.
 */
export function createWalletTokenLayout(
  count = MAX_BALANCE_PARTICLES,
  seed = createWalletTokenSeed()
): WalletTokenPoint[] {
  const random = createSeededRandom(seed)
  const safeCount = Math.max(0, Math.floor(Number.isFinite(count) ? count : 0))
  const lobeCount = 2 + randomInt(random, 4)
  const phase = random() * Math.PI * 2
  const centers = Array.from({ length: lobeCount }, (_, index) => {
    const angle =
      phase +
      (index / lobeCount) * Math.PI * 2 +
      (random() - 0.5) * (Math.PI / lobeCount)
    const radius = 0.08 + random() * 0.28
    return {
      x: Math.cos(angle) * radius,
      y: Math.sin(angle) * radius * (0.45 + random() * 0.25),
      spreadX: 0.34 + random() * 0.24,
      spreadY: 0.42 + random() * 0.3,
    }
  })

  return Array.from({ length: safeCount }, () => {
    const center = centers[randomInt(random, centers.length)]
    const angle = random() * Math.PI * 2
    const radius = Math.sqrt(random())
    const jitterX = (random() - 0.5) * 0.1
    const jitterY = (random() - 0.5) * 0.12
    const depth = Math.max(
      0,
      Math.min(1, 0.22 + (1 - radius * 0.58) * 0.72 + (random() - 0.5) * 0.24)
    )

    return {
      x: Math.max(
        -1,
        Math.min(
          1,
          center.x + Math.cos(angle) * radius * center.spreadX + jitterX
        )
      ),
      y: Math.max(
        -1,
        Math.min(
          1,
          center.y + Math.sin(angle) * radius * center.spreadY + jitterY
        )
      ),
      depth,
      glyph: createWalletTokenGlyph(random),
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
