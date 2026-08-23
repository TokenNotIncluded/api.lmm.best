/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
const WARNING_MODES = new Set(['modal', 'banner', 'inline'])

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function isValidGroupWarnings(value: unknown): boolean {
  if (!isPlainObject(value)) return false

  return Object.entries(value).every(([group, warning]) => {
    const groupName = group.trim()
    if (!groupName || [...groupName].length > 64) return false
    if (!isPlainObject(warning)) return false

    const enabled = warning.enabled
    if (enabled !== undefined && typeof enabled !== 'boolean') return false

    const message = warning.message
    if (message !== undefined && typeof message !== 'string') return false
    if (typeof message === 'string' && [...message].length > 2000) {
      return false
    }
    if (enabled === true && (typeof message !== 'string' || !message.trim())) {
      return false
    }

    const mode = warning.mode
    if (
      mode !== undefined &&
      (typeof mode !== 'string' ||
        (mode.trim() !== '' && !WARNING_MODES.has(mode.trim().toLowerCase())))
    ) {
      return false
    }

    const confirmations = warning.confirmations
    return (
      confirmations === undefined ||
      (typeof confirmations === 'number' &&
        Number.isInteger(confirmations) &&
        confirmations >= 1 &&
        confirmations <= 3)
    )
  })
}
