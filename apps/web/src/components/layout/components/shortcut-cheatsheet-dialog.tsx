/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Kbd, KbdGroup } from '@/components/ui/kbd'

import {
  CONSOLE_SHORTCUTS,
  SHORTCUT_GROUP_ORDER,
  SHORTCUT_GROUP_TITLES,
  type ShortcutDefinition,
} from '../lib/shortcuts'
import { useQuickSwitch } from './quick-switch-provider'

function ShortcutRow({
  shortcut,
  onNavigate,
}: {
  shortcut: ShortcutDefinition
  onNavigate?: (to: string) => void
}) {
  const { t } = useTranslation()
  const label = t(shortcut.id)

  return (
    <li className='flex min-h-11 items-center justify-between gap-4 py-1.5'>
      {shortcut.to ? (
        <button
          type='button'
          className='text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 -mx-1.5 min-w-0 truncate rounded-md px-1.5 py-1 text-start text-sm transition-colors outline-none focus-visible:ring-[3px] motion-reduce:transition-none'
          onClick={() => onNavigate?.(shortcut.to as string)}
        >
          {label}
        </button>
      ) : (
        <span className='text-muted-foreground min-w-0 truncate text-sm'>
          {label}
        </span>
      )}
      <KbdGroup className='shrink-0'>
        {shortcut.keys.map((key, index) => (
          <Kbd key={`${shortcut.id}-${index}`}>
            {key === '⌘' &&
            typeof navigator !== 'undefined' &&
            !/Mac|iPhone|iPad/.test(navigator.platform)
              ? 'Ctrl'
              : key}
          </Kbd>
        ))}
      </KbdGroup>
    </li>
  )
}

/** The `?` keyboard shortcut cheat sheet for the signed-in console. */
export function ShortcutCheatsheetDialog() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { shortcutSheetOpen, setShortcutSheetOpen } = useQuickSwitch()

  return (
    <Dialog open={shortcutSheetOpen} onOpenChange={setShortcutSheetOpen}>
      <DialogContent
        className='top-[10dvh] max-h-[min(80dvh,40rem)] max-w-[min(calc(100%-2rem),32rem)] translate-y-0 overflow-y-auto overscroll-contain'
        data-testid='shortcut-cheatsheet'
      >
        <DialogHeader>
          <DialogTitle>{t('Keyboard shortcuts')}</DialogTitle>
          <DialogDescription>
            {t('Press ? anywhere to reopen this list.')}
          </DialogDescription>
        </DialogHeader>
        <div className='flex flex-col gap-5'>
          {SHORTCUT_GROUP_ORDER.map((group) => {
            const shortcuts = CONSOLE_SHORTCUTS.filter(
              (shortcut) => shortcut.group === group
            )
            if (shortcuts.length === 0) return null
            return (
              <section key={group} className='flex flex-col gap-1'>
                <h3 className='text-muted-foreground text-[11px] font-medium tracking-wider uppercase'>
                  {t(SHORTCUT_GROUP_TITLES[group])}
                </h3>
                <ul className='divide-border/60 flex flex-col divide-y'>
                  {shortcuts.map((shortcut) => (
                    <ShortcutRow
                      key={shortcut.id}
                      shortcut={shortcut}
                      onNavigate={(to) => {
                        setShortcutSheetOpen(false)
                        void navigate({ to })
                      }}
                    />
                  ))}
                </ul>
              </section>
            )
          })}
        </div>
      </DialogContent>
    </Dialog>
  )
}
