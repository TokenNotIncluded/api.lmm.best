/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import { Check, Copy } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export function InstallCommand({
  value,
  copyLabel,
}: {
  value: string
  copyLabel: string
}) {
  const { t } = useTranslation()
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = async () => {
    clearTimeout(timer.current)
    try {
      await navigator.clipboard.writeText(value)
      setState('copied')
      timer.current = setTimeout(() => setState('idle'), 1600)
    } catch {
      setState('failed')
    }
  }

  return (
    <>
      <div className='flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center'>
        <code
          tabIndex={0}
          className='bg-muted min-w-0 flex-1 overflow-x-auto rounded px-3 py-2 text-xs select-text'
        >
          {value}
        </code>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void copy()}
          aria-label={copyLabel}
          className='shrink-0'
        >
          {state === 'copied' ? (
            <Check data-icon='inline-start' />
          ) : (
            <Copy data-icon='inline-start' />
          )}
          {state === 'copied' ? t('Copied') : t('Copy')}
        </Button>
      </div>
      {state === 'failed' && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Unable to copy command')}
        </p>
      )}
    </>
  )
}
