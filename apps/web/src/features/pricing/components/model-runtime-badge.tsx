/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useTranslation } from 'react-i18next'

import type { ModelRuntimeState } from '../types'

const MODEL_RUNTIME_LABELS = {
  available: 'Routable now',
  congested: 'Congested',
  maintenance: 'Under maintenance',
  unavailable: 'Temporarily unavailable',
  no_access: 'No account access',
  not_listed: 'Not in the model catalog',
  unknown: 'Runtime status unknown',
} as const
export function ModelRuntimeBadge({ state }: { state?: ModelRuntimeState }) {
  const { t } = useTranslation()
  return (
    <span
      className='text-muted-foreground text-xs'
      title={
        state?.source === 'administrator_notice'
          ? t('Administrator status notice')
          : state?.source === 'account_permissions'
            ? t('API access')
            : t('Current routing configuration')
      }
    >
      {t(MODEL_RUNTIME_LABELS[state?.status ?? 'unknown'])}
    </span>
  )
}
