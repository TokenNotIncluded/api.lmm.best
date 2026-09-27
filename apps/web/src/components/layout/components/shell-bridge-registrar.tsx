/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect } from 'react'

import { registerShellBridge } from '../lib/shell-bridge'
import { useQuickSwitchOptional } from './quick-switch-provider'

/**
 * Publishes the signed-in shell's live controls to the WebMCP bridge.
 *
 * Renders nothing. The bridge only opens navigation panels.
 */
export function ShellBridgeRegistrar() {
  const quickSwitch = useQuickSwitchOptional()

  useEffect(() => {
    const openPalette = quickSwitch?.openPalette
    const openShortcutSheet = () => quickSwitch?.openShortcuts()
    return registerShellBridge({
      ...(openPalette ? { openPalette } : {}),
      ...(quickSwitch ? { openShortcutSheet } : {}),
    })
  }, [quickSwitch])

  return null
}
