/*
Copyright (C) 2026 LIghtJUNction
*/
export function buildToolPriceOverrides(
  prices: Record<string, number>,
  savedOverrides: string,
  defaults: Record<string, number>
): Record<string, number> {
  let saved: Record<string, unknown> = {}
  try {
    const parsed: unknown = JSON.parse(savedOverrides || '{}')
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      saved = parsed as Record<string, unknown>
    }
  } catch {
    // Invalid drafts are blocked by the editor before saving.
  }
  return Object.fromEntries(
    Object.entries(prices).filter(
      ([key, value]) =>
        Object.hasOwn(saved, key) ||
        !Object.hasOwn(defaults, key) ||
        value !== defaults[key]
    )
  )
}
