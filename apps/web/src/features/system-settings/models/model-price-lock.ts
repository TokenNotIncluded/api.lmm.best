/*
Copyright (C) 2026 LIghtJUNction
*/

export function parseModelPriceLocks(value: string): Record<string, boolean> {
  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(([, locked]) => locked === true)
    )
  } catch {
    return {}
  }
}

// Keep shared price aliases aligned with the backend's FormatMatchingModelName.
function matchingModelName(name: string): string {
  for (const prefix of [
    'gemini-2.5-flash-lite',
    'gemini-2.5-flash',
    'gemini-2.5-pro',
  ]) {
    if (name.startsWith(prefix)) {
      return name.includes('-thinking-') ? `${prefix}-thinking-*` : name
    }
  }
  if (name.startsWith('gpt-4-gizmo')) return 'gpt-4-gizmo-*'
  if (name.startsWith('gpt-4o-gizmo')) return 'gpt-4o-gizmo-*'
  return name
}

export function isModelPriceLocked(
  locks: Record<string, boolean>,
  name: string
): boolean {
  const matching = matchingModelName(name)
  return Object.entries(locks).some(
    ([model, locked]) => locked && matchingModelName(model) === matching
  )
}

export function withModelPriceLock(
  locks: Record<string, boolean>,
  name: string,
  locked: boolean
): Record<string, boolean> {
  if (locked) return { ...locks, [name]: true }
  const matching = matchingModelName(name)
  return Object.fromEntries(
    Object.entries(locks).filter(
      ([model]) => matchingModelName(model) !== matching
    )
  )
}
