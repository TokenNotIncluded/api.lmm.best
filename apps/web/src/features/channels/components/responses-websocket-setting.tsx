/*
Copyright (C) 2026 LIghtJUNction
*/
import type { Control } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import type { SystemStatus } from '@/features/auth/types'
import { getBackendCapabilities } from '@/lib/backend-capabilities'

import {
  getDefaultResponsesWebSocketEnabled,
  supportsResponsesWebSocket,
} from '../constants'
import type { ChannelFormValues } from '../lib/channel-form'

export function ResponsesWebSocketSetting({
  control,
  channelType,
  status,
}: {
  control: Control<ChannelFormValues>
  channelType: number
  status: SystemStatus | null | undefined
}) {
  const { t } = useTranslation()
  if (!supportsResponsesWebSocket(channelType)) return null

  const available = getBackendCapabilities(status).responses_websocket

  return (
    <FormField
      control={control}
      name='responses_websocket_enabled'
      render={({ field }) => (
        <FormItem className='flex items-center justify-between gap-4 px-4 py-3'>
          <div className='space-y-0.5'>
            <FormLabel>{t('Responses WebSocket')}</FormLabel>
            <FormDescription>
              {t('Allow persistent connections to /v1/responses.')}
              {channelType === 58 && (
                <span className='block'>
                  {t('Requires a /v1/responses route with no converter.')}
                </span>
              )}
              {!available && (
                <span className='block'>
                  {t(
                    'Responses WebSocket is unavailable on the current backend.'
                  )}
                </span>
              )}
            </FormDescription>
            <FormMessage />
          </div>
          <FormControl>
            <Switch
              checked={
                available &&
                (field.value ??
                  getDefaultResponsesWebSocketEnabled(channelType))
              }
              onCheckedChange={field.onChange}
              disabled={!available}
            />
          </FormControl>
        </FormItem>
      )}
    />
  )
}
