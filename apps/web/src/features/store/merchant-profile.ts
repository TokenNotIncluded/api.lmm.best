/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export function storeSellerId(value: unknown): number | undefined {
  const text =
    typeof value === 'number' || typeof value === 'string' ? String(value) : ''
  if (!/^[1-9]\d{0,9}$/.test(text)) return undefined
  const id = Number(text)
  return Number.isSafeInteger(id) && id <= 2147483647 ? id : undefined
}

export function storeSellerMailto(value: unknown): string | undefined {
  if (
    typeof value !== 'string' ||
    value.length > 320 ||
    /\s/.test(value) ||
    [...value].some((character) => {
      const code = character.charCodeAt(0)
      return code < 32 || (code >= 127 && code <= 159)
    }) ||
    !/^[^@]+@[^@]+$/.test(value)
  ) {
    return undefined
  }
  try {
    return `mailto:${encodeURIComponent(value)}`
  } catch {
    return undefined
  }
}
