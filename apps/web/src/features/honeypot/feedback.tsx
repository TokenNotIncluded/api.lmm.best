/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { HoneypotFailure } from './api'

function honeypotErrorKey(error: unknown): string {
  switch (error instanceof HoneypotFailure ? error.kind : 'invalid') {
    case 'conflict':
      return 'Settings or users changed on the server. Reload and review again.'
    case 'expired':
      return 'Preview expired. Review again.'
    case 'unknown':
      return 'Save result is unknown. Check the original operation before continuing.'
    case 'forbidden':
      return 'Super administrator access required.'
    case 'not_found':
      return 'The original operation is not recorded yet. Check again before continuing.'
    case 'unavailable':
      return 'Server support is unavailable.'
    default:
      return 'Configuration or selection is invalid. Check the fields and limits.'
  }
}

export function HoneypotFeedback(props: {
  error?: unknown
  operationID?: string
  check?: () => void
  checking?: boolean
}) {
  const { t } = useTranslation()
  if (!props.error) return null
  return (
    <Alert variant='destructive' role='alert'>
      <AlertDescription className='space-y-2'>
        <p>{t(honeypotErrorKey(props.error))}</p>
        {props.operationID && (
          <p className='font-mono text-xs break-all'>
            {t('Operation {{id}}', { id: props.operationID })}
          </p>
        )}
        {props.check && (
          <Button
            variant='outline'
            type='button'
            disabled={props.checking}
            onClick={props.check}
          >
            {t('Check original operation')}
          </Button>
        )}
      </AlertDescription>
    </Alert>
  )
}
