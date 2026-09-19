/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export function formatGameTime(ms: number | null) {
  if (ms === null) return '—'
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}.${Math.floor((ms % 1000) / 100)}`
}
