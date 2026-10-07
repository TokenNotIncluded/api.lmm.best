/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getSettingsErrorMessage } from '@/features/system-settings/utils/settings-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { marketAIReviewAPI } from './api'
import { MARKET_AI_REVIEW_COPY as copy } from './copy'
import { MarketAIReviewModeFields } from './mode-fields'
import { MarketAIReviewSettingsForm } from './settings-form'

export function MarketAIReviewSettingsSection() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: ['market-ai-review-settings', user?.id],
    queryFn: marketAIReviewAPI.settings,
    enabled: (user?.role ?? 0) >= 10,
    retry: false,
  })
  if ((user?.role ?? 0) < 10) return null
  if (query.isPending) return <p role='status'>{t('Loading...')}</p>
  if (query.isError) {
    return (
      <div role='alert' className='space-y-3'>
        <p>
          {getSettingsErrorMessage(query.error, t('Failed to load settings'))}
        </p>
        <Button variant='outline' onClick={() => void query.refetch()}>
          {t('Retry')}
        </Button>
      </div>
    )
  }
  const settings = query.data
  const modes = { tool: settings.tool_mode, product: settings.store_mode }
  return (
    <div className='space-y-5'>
      <p className='text-muted-foreground text-sm'>
        {t('Group')}: {settings.review_group} · {t('Model')}:{' '}
        {settings.review_model}
      </p>
      {(user?.role ?? 0) >= 100 ? (
        <MarketAIReviewSettingsForm
          defaultValues={modes}
          onSave={async (updates) => {
            const saved = await marketAIReviewAPI.saveSettings({
              tool_mode: updates.tool ?? settings.tool_mode,
              store_mode: updates.product ?? settings.store_mode,
            })
            client.setQueryData(['market-ai-review-settings', user?.id], saved)
            await client.invalidateQueries({
              queryKey: ['market-ai-review-settings'],
            })
          }}
        />
      ) : (
        <MarketAIReviewModeFields value={modes} onChange={() => {}} disabled />
      )}
      <a
        className='text-primary text-sm underline'
        href='/system-settings/security/moderation'
      >
        {t(copy.routing)}
      </a>
    </div>
  )
}
