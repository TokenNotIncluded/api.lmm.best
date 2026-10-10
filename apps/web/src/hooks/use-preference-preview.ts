/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useState } from 'react'

/** Temporary display state never changes the saved preference or its cookie. */
export function usePreferencePreview<T>(saved: T) {
  const [entry, setEntry] = useState<{ id: symbol; value: T } | null>(null)
  const clear = useCallback(() => setEntry(null), [])
  const preview = useCallback((value: T) => {
    const id = Symbol('preference-preview')
    setEntry({ id, value })
    // An older timeout must not cancel a newer preview or manual choice.
    return () => setEntry((current) => (current?.id === id ? null : current))
  }, [])
  return { value: entry ? entry.value : saved, preview, clear }
}
