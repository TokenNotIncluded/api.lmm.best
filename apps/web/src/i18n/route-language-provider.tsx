/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import type { ReactNode } from 'react'
import { I18nextProvider } from 'react-i18next'

import appI18n from './config'

/** Share the selected language across routes, including root footer/overlays. */
export function RouteLanguageProvider({ children }: { children: ReactNode }) {
  return <I18nextProvider i18n={appI18n}>{children}</I18nextProvider>
}
