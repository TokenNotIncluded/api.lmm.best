/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'

import { AssistantHandoffTool } from './assistant-handoff-tool'

/**
 * A history trace is not a signed action. Recover through the existing manual
 * support form instead of replaying model arguments or inventing a token.
 */
export function AssistantSupportReview() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <div className='px-3 pb-3'>
        <DialogTrigger
          render={<Button type='button' variant='outline' />}
          data-testid='assistant-support-review'
        >
          {t('Send a message to an administrator')}
        </DialogTrigger>
      </div>
      <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle>{t('Human technical support')}</DialogTitle>
          <DialogDescription>
            {t(
              'The message will be stored for administrators. Do not include passwords, API keys, or session cookies.'
            )}
          </DialogDescription>
        </DialogHeader>
        {open ? <AssistantHandoffTool /> : null}
      </DialogContent>
    </Dialog>
  )
}
