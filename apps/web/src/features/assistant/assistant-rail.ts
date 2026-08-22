/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, you have received a copy of the
License, or (at your option) any later version.
*/

/**
 * Desktop assistant rail state.
 *
 * The console shell renders the assistant as an in-flow right panel (not an
 * overlay), so the header chat button needs to toggle it from outside the
 * authenticated layout. This tiny external store keeps that in sync without
 * lifting state through the route tree.
 */
import { useSyncExternalStore } from 'react'

const RAIL_OPEN_EVENT = 'lmm:assistant-rail-did-change'

let railOpen = false
const listeners = new Set<() => void>()

function emit() {
  for (const listener of listeners) listener()
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(RAIL_OPEN_EVENT))
  }
}

export function setAssistantRailOpen(open: boolean): void {
  if (railOpen === open) return
  railOpen = open
  emit()
}

export function toggleAssistantRail(): boolean {
  railOpen = !railOpen
  emit()
  return railOpen
}

export function isAssistantRailOpen(): boolean {
  return railOpen
}

export function subscribeAssistantRail(callback: () => void): () => void {
  listeners.add(callback)
  return () => listeners.delete(callback)
}

export function useAssistantRailOpen(): boolean {
  return useSyncExternalStore(
    subscribeAssistantRail,
    isAssistantRailOpen,
    () => false
  )
}

/** Live CSS-var-free read for non-React callers (e.g. header button label). */
export function onAssistantRailChange(callback: () => void): () => void {
  if (typeof window === 'undefined') return () => undefined
  window.addEventListener(RAIL_OPEN_EVENT, callback)
  return () => window.removeEventListener(RAIL_OPEN_EVENT, callback)
}
