/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { t } from 'i18next'

import { rateLimitMessage } from '@/lib/request-rate-limit'
import { getServerErrorMessageKey } from '@/lib/server-error-message'

export function twoFAErrorMessage(error: unknown, fallback: string): string {
  const key = getServerErrorMessageKey(error)
  return rateLimitMessage(error) ?? (key ? t(key) : fallback)
}
