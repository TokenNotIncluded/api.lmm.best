/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
/**
 * Live handles to shell controls that only exist inside React context.
 *
 * Keeping these handles here avoids importing the React provider tree from
 * WebMCP. Only navigation panels are exposed; settings cannot be changed.
 */
export type ShellBridge = {
  openPalette: () => void
  openShortcutSheet: () => void
}

let bridge: Partial<ShellBridge> = {}

export function registerShellBridge(handles: Partial<ShellBridge>): () => void {
  bridge = { ...bridge, ...handles }
  return () => {
    bridge = {}
  }
}

export function getShellBridge(): Partial<ShellBridge> {
  return bridge
}
