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
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  buildAssistantClientImport,
  type AssistantImportApp,
} from './client-import'

export function ClientKeyImport(props: {
  rootUrl: string
  openAIBaseUrl: string
  model: string
  availableModels: string[]
  modelsLoading?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const keyInput = useRef<HTMLInputElement>(null)
  const [open, setOpen] = useState(false)
  const [hasKey, setHasKey] = useState(false)
  const [app, setApp] = useState<AssistantImportApp>('claude')
  const [status, setStatus] = useState<'idle' | 'opened' | 'failed'>('idle')
  const modelReady =
    !props.modelsLoading && props.availableModels.includes(props.model)

  const clear = () => {
    if (keyInput.current) keyInput.current.value = ''
    setHasKey(false)
  }
  const importKey = () => {
    const url = buildAssistantClientImport({
      ...props,
      app,
      apiKey: keyInput.current?.value ?? '',
      currentOrigin: window.location.origin,
    })
    clear()
    if (!url) {
      setStatus('failed')
      return
    }
    // The private key remains in the temporary input until this click. It is
    // never inserted into chat, persistent storage, or a rendered href.
    try {
      window.location.assign(url)
      setStatus('opened')
    } catch {
      setStatus('failed')
    }
  }

  return (
    <div className='bg-muted/30 grid gap-3 rounded-xl border p-4'>
      <div>
        <p className='text-sm font-medium'>{t('CC Switch one-click import')}</p>
        <p className='text-muted-foreground mt-1 text-sm leading-6'>
          {t(
            'Install CC Switch first. You can import an existing key, then review and enable the provider in the app.'
          )}
        </p>
      </div>
      {!open ? (
        <Button
          type='button'
          variant='outline'
          className='min-h-10 w-fit'
          onClick={() => setOpen(true)}
        >
          {t('Use an existing API key')}
        </Button>
      ) : (
        <div className='grid gap-3'>
          <label className='grid gap-2 text-sm'>
            {t('Import target')}
            <NativeSelect
              value={app}
              onChange={(event) => {
                clear()
                setApp(event.target.value as AssistantImportApp)
                setStatus('idle')
              }}
            >
              <NativeSelectOption value='claude'>
                Claude Code
              </NativeSelectOption>
              <NativeSelectOption value='codex'>Codex</NativeSelectOption>
            </NativeSelect>
          </label>
          <label className='grid gap-2 text-sm' htmlFor={id}>
            {t('API key for this import only')}
            <Input
              ref={keyInput}
              id={id}
              type='password'
              autoComplete='off'
              spellCheck={false}
              aria-describedby={`${id}-privacy`}
              className='min-h-11'
              placeholder='sk-…'
              onChange={(event) => {
                setHasKey(Boolean(event.target.value.trim()))
                setStatus('idle')
              }}
            />
          </label>
          <p
            id={`${id}-privacy`}
            className='text-muted-foreground text-xs leading-5'
          >
            {t(
              'Clicking Open CC Switch passes this key to the installed app through its local protocol. The input is then cleared. This page does not save it or send it to the assistant. Only continue if you trust the installed app.'
            )}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              className='min-h-10'
              disabled={!hasKey || !modelReady}
              onClick={importKey}
            >
              {t('Open CC Switch')}
            </Button>
            <Button
              type='button'
              variant='ghost'
              className='min-h-10'
              onClick={() => {
                clear()
                setOpen(false)
                setStatus('idle')
              }}
            >
              {t('Cancel')}
            </Button>
          </div>
          {!modelReady ? (
            <p className='text-muted-foreground text-xs'>
              {t('Wait for an available model before importing.')}
            </p>
          ) : null}
        </div>
      )}
      {status !== 'idle' ? (
        <p role='status' className='text-muted-foreground text-sm leading-6'>
          {status === 'opened'
            ? t(
                'Open request sent. Confirm the provider in CC Switch; this page cannot detect whether the app imported it. If nothing opens, use the manual values below.'
              )
            : t(
                'The import could not be opened. Check the service address and use the manual values below.'
              )}
        </p>
      ) : null}
    </div>
  )
}
