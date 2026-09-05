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
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { createContext, useCallback, useContext, useRef, useState } from 'react'

type ModelPlazaContextValue = {
  open: boolean
  openPanel: (trigger?: HTMLElement | null) => void
  closePanel: () => void
}

const ModelPlazaContext = createContext<ModelPlazaContextValue | null>(null)

export function ModelPlazaProvider({
  children,
}: {
  children: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLElement | null>(null)

  const openPanel = useCallback((trigger?: HTMLElement | null) => {
    triggerRef.current =
      trigger ??
      (typeof document !== 'undefined'
        ? (document.activeElement as HTMLElement | null)
        : null)
    setOpen(true)
  }, [])

  const closePanel = useCallback(() => {
    setOpen(false)
    const trigger = triggerRef.current
    triggerRef.current = null
    if (trigger) {
      requestAnimationFrame(() => {
        if (document.contains(trigger)) trigger.focus()
      })
    }
  }, [])

  return (
    <ModelPlazaContext value={{ open, openPanel, closePanel }}>
      {children}
    </ModelPlazaContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useModelPlaza() {
  const context = useContext(ModelPlazaContext)
  if (!context) {
    throw new Error('useModelPlaza must be used within ModelPlazaProvider')
  }
  return context
}
