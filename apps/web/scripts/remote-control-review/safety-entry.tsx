/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Test-only entry: the real page, store, queries and encryption; synthetic users.

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import i18n from 'i18next'
import React from 'react'
import { createRoot } from 'react-dom/client'
import { initReactI18next, I18nextProvider } from 'react-i18next'

import { RemoteControl } from '../../src/features/remote-control'
import zh from '../../src/i18n/locales/zh.json'
import { useAuthStore, type AuthUser } from '../../src/stores/auth-store'

const locale = i18n.createInstance()
await locale
  .use(initReactI18next)
  .init({ lng: 'zh', resources: { zh }, interpolation: { escapeValue: false } })
const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
Object.assign(window, {
  remoteReview: {
    setUser: (id?: number) =>
      id
        ? useAuthStore.getState().auth.setUser({ id } as AuthUser)
        : useAuthStore.getState().auth.reset(),
    cachedOwners: () =>
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
  },
})
const root = document.getElementById('root')
if (!root) throw new Error('Missing review root')
createRoot(root).render(
  <QueryClientProvider client={client}>
    <I18nextProvider i18n={locale}>
      <RemoteControl />
    </I18nextProvider>
  </QueryClientProvider>
)
