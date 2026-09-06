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
import { Loading03Icon, ShieldKeyIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { copyToClipboard } from '@/lib/copy-to-clipboard'
import { openResolvedExternalUrl } from '@/lib/external-navigation'

import { revealAssistantPrivateCard, type AssistantPrivateCard } from './api'

export function AssistantPrivateCard(props: {
  card: AssistantPrivateCard
  onContinue: () => void
  onImportToCCSwitch?: (
    secret: string
  ) => string | null | Promise<string | null>
}) {
  const { t } = useTranslation()
  const secretRef = useRef('')
  const [viewing, setViewing] = useState(false)
  const [loading, setLoading] = useState(false)
  const [importing, setImporting] = useState(false)

  const getSecret = async (): Promise<string | null> => {
    if (secretRef.current) return secretRef.current
    setLoading(true)
    try {
      const secret = await revealAssistantPrivateCard(props.card.id)
      secretRef.current = secret
      return secret
    } catch {
      toast.error(t('Unable to retrieve the private credential.'))
      return null
    } finally {
      setLoading(false)
    }
  }

  const showSecret = async () => {
    const secret = await getSecret()
    if (secret) setViewing(true)
  }

  const copySecret = async () => {
    const secret = await getSecret()
    if (!secret) return
    const copied = await copyToClipboard(secret)
    if (copied) {
      toast.success(t('Copied to clipboard'))
      secretRef.current = ''
      setViewing(false)
      return
    }
    toast.error(t('Failed to copy'))
  }

  const hideSecret = () => {
    secretRef.current = ''
    setViewing(false)
  }

  const importToCCSwitch = async () => {
    if (!props.onImportToCCSwitch || importing) return
    setImporting(true)
    try {
      let credentialResolved = false
      const opened = await openResolvedExternalUrl(async () => {
        const secret = await getSecret()
        if (!secret) return null
        credentialResolved = true
        return props.onImportToCCSwitch?.(secret) ?? null
      })
      if (!opened) {
        if (credentialResolved) toast.error(t('Unable to open CC Switch'))
        return
      }
      secretRef.current = ''
      setViewing(false)
    } catch {
      toast.error(t('Unable to open CC Switch'))
    } finally {
      setImporting(false)
    }
  }

  return (
    <div
      className='border-success/40 bg-success/5 grid gap-3 rounded-lg border p-3'
      data-testid='assistant-private-card'
    >
      <div className='flex items-start gap-2'>
        <HugeiconsIcon
          icon={ShieldKeyIcon}
          className='text-success mt-0.5 size-4 shrink-0'
          strokeWidth={2}
          aria-hidden='true'
        />
        <div className='min-w-0'>
          <p className='text-sm font-medium'>
            {props.card.label || t('Private credential')}
          </p>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {t(
              'This card is visible only to you. Credentials are never written into chat history.'
            )}
          </p>
        </div>
      </div>
      {viewing && secretRef.current ? (
        <code
          className='bg-background/80 block rounded-md border px-2 py-2 text-xs break-all'
          data-testid='assistant-private-card-value'
        >
          {secretRef.current}
        </code>
      ) : null}
      <div className='flex flex-wrap gap-2'>
        {props.onImportToCCSwitch ? (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void importToCCSwitch()}
            disabled={loading || importing}
          >
            {importing ? (
              <HugeiconsIcon
                icon={Loading03Icon}
                className='mr-1 animate-spin'
                strokeWidth={2}
                aria-hidden='true'
              />
            ) : null}
            {t('Import to CC Switch')}
          </Button>
        ) : null}
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void copySecret()}
          disabled={loading || importing}
        >
          {loading ? (
            <HugeiconsIcon
              icon={Loading03Icon}
              className='mr-1 animate-spin'
              strokeWidth={2}
              aria-hidden='true'
            />
          ) : null}
          {t('Copy securely')}
        </Button>
        {viewing ? (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={hideSecret}
          >
            {t('Hide credential')}
          </Button>
        ) : (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void showSecret()}
            disabled={loading || importing}
          >
            {t('Show securely')}
          </Button>
        )}
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={props.onContinue}
        >
          {t('I copied it — continue setup')}
        </Button>
      </div>
    </div>
  )
}
