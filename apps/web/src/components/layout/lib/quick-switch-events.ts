/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
type Listener = () => void

const listeners = new Set<Listener>()

/**
 * Request the search provider to open the ⌘K palette.
 *
 * The shortcut sheet lives inside the quick-switch tree, which is rendered by
 * the authenticated layout above the search provider — calling `useSearch()`
 * there would close the layout ↔ command-menu import cycle. A tiny event bus
 * keeps the palette owner as the single source of truth instead.
 */
export function requestPaletteOpen() {
  for (const listener of listeners) listener()
}

export function onPaletteOpenRequest(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}
