/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import { AssistantHandoffTool } from './assistant-handoff-tool'

// Traces can be restored from history. Never replay their input or an old
// confirmation token: recovery starts a fresh message and still requires review.
export function AssistantSupportReviewDialog() {
  const { t } = useTranslation()
  const messageInputId = useId()
  const [open, setOpen] = useState(false)

  return (
    <>
      <Button
        type='button'
        size='sm'
        variant='outline'
        className='min-h-11 whitespace-normal sm:min-h-9'
        onClick={() => setOpen(true)}
      >
        {t('Send a message to an administrator')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-xl'>
          <DialogHeader>
            <DialogTitle>{t('Human technical support')}</DialogTitle>
            <DialogDescription>
              {t(
                'The message will be stored for administrators. Do not include passwords, API keys, or session cookies.'
              )}
            </DialogDescription>
          </DialogHeader>
          {open ? (
            <AssistantHandoffTool
              confirmationAction={null}
              messageInputId={messageInputId}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  )
}
