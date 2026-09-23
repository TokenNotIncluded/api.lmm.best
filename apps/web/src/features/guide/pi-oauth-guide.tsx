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
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

const PI_INSTALL_COMMAND =
  'npm exec --yes --package=@tokennotincluded/pi-lmm-provider@0.1.0-alpha.2 -- lmm-pi-provider npm:@tokennotincluded/pi-lmm-provider@0.1.0-alpha.2'

export function PiOAuthGuide() {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(PI_INSTALL_COMMAND)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1400)
    } catch {
      setCopied(false)
    }
  }
  return (
    <section id='pi-oauth' className='scroll-mt-24 space-y-4 border-y py-6'>
      <div>
        <p className='text-muted-foreground text-xs font-semibold tracking-[0.16em] uppercase'>
          {t('OAuth2 with Pi · no API key')}
        </p>
        <h2 className='mt-2 text-xl font-semibold'>
          {t('Use Pi without manually creating an API key')}
        </h2>
        <p className='text-muted-foreground mt-2 text-sm leading-7'>
          {t(
            'Install the LMM Pi plugin, sign in with OAuth, and choose a model in Pi. Access uses your account and normal model pricing.'
          )}
        </p>
      </div>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center'>
        <code className='bg-muted min-w-0 flex-1 overflow-x-auto rounded px-3 py-2 text-xs'>
          {PI_INSTALL_COMMAND}
        </code>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={() => void copy()}
          className='shrink-0'
        >
          {copied ? (
            <Check data-icon='inline-start' />
          ) : (
            <Copy data-icon='inline-start' />
          )}
          {copied ? t('Copied') : t('Copy')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm leading-7'>
        {t(
          'After installing, use /login in Pi and choose LMM, then select a model with /model. /lmm-prices is optional for checking current prices; /lmm-revoke is an optional server authorization reset.'
        )}
      </p>
      <a
        href='https://www.npmjs.com/package/@earendil-works/pi-coding-agent'
        target='_blank'
        rel='noreferrer'
        className='text-sm underline underline-offset-4'
      >
        {t('Pi official package information')}
      </a>
    </section>
  )
}
