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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { Copy, Check } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { ScrollArea } from '@/components/ui/scroll-area'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

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
import { safeLogDiagnostic } from '../../lib/recovery'

interface FailReasonDialogProps {
  failReason: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function FailReasonDialog({
  failReason,
  open,
  onOpenChange,
}: FailReasonDialogProps) {
  const { t } = useTranslation()
  const safeReason = safeLogDiagnostic(failReason)
  const { copiedText, copyToClipboard } = useCopyToClipboard({ notify: false })

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Fail Reason Details')}
      description={t('View the complete error message and details')}
      contentClassName='sm:max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <Button
        type='button'
        variant='outline'
        onClick={() => {
          onOpenChange(false)
          requestAssistantOpen(
            'usage',
            `${t('Help me diagnose this API request.')}\n${safeReason}`
          )
        }}
      >
        {t('Ask AI assistant')}
      </Button>
      <p className='text-muted-foreground text-sm'>
        {t('Only diagnostic metadata is copied; raw error bodies are omitted.')}
      </p>
      <ScrollArea className='max-h-[500px] pr-4'>
        <div className='space-y-4 py-4'>
          <div className='space-y-2'>
            <Label className='text-sm font-semibold'>
              {t('Error Message')}
            </Label>
            <div className='console-log-danger-panel bg-muted/50 relative rounded-md border p-3'>
              <Button
                variant='ghost'
                size='sm'
                className='absolute top-2 right-2 h-8 w-8 p-0'
                onClick={() => copyToClipboard(safeReason)}
                title={t('Copy to clipboard')}
              >
                {copiedText === safeReason ? (
                  <Check className='console-status-success-icon size-4' />
                ) : (
                  <Copy className='size-4' />
                )}
              </Button>
              <p className='console-status-danger-text overflow-wrap-anywhere pr-10 text-sm leading-relaxed break-all whitespace-pre-wrap'>
                {safeReason || '-'}
              </p>
            </div>
          </div>
        </div>
      </ScrollArea>
    </Dialog>
  )
}
