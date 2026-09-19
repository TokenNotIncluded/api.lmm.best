/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useRouterState } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'

import appI18n from './config'
import landingI18n from './landing-i18n'

/** Includes the root footer/overlays while leaving the saved preference alone. */
export function RouteLanguageProvider({ children }: { children: ReactNode }) {
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  return (
    <I18nextProvider i18n={pathname === '/' ? landingI18n : appI18n}>
      {children}
    </I18nextProvider>
  )
}
