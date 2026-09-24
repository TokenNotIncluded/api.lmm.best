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
import { useTranslation } from 'react-i18next'

import { InstallCommand } from './install-command'
import {
  CODEWHALE_ADAPTER_INSTALL_COMMAND,
  CODEWHALE_CLONE_COMMAND,
  CODEWHALE_LOGIN_COMMAND,
  CODEWHALE_MODELS_COMMAND,
  CODEWHALE_RUN_COMMAND,
  CODEWHALE_UNIX_INSTALL_COMMAND,
} from './provider-install-commands'

const CODEWHALE_RELEASES = 'https://github.com/Hmbown/CodeWhale/releases/latest'
const CODEWHALE_INSTALL_DOCS =
  'https://github.com/Hmbown/CodeWhale/blob/main/docs/INSTALL.md'
const CODEWHALE_PROVIDER =
  'https://github.com/TokenNotIncluded/codewhale-lmm-provider'

export function CodewhaleOAuthGuide({
  windows = false,
}: {
  windows?: boolean
}) {
  const { t } = useTranslation()

  return (
    <section
      id='codewhale-oauth'
      className='scroll-mt-24 space-y-5 border-y py-6'
    >
      <div>
        <p className='text-muted-foreground text-xs font-semibold tracking-[0.16em] uppercase'>
          {t('OAuth with Codewhale · no API key')}
        </p>
        <h2 className='mt-2 text-xl font-semibold'>
          {t('Use LMM in Codewhale')}
        </h2>
        <p className='text-muted-foreground mt-2 text-sm leading-7'>
          {t(
            'Codewhale currently connects through the codewhale-lmm companion adapter. It opens LMM OAuth in your browser and does not ask you to paste an API key.'
          )}
        </p>
      </div>

      <div className='space-y-2'>
        <h3 className='text-sm font-semibold'>{t('1. Install Codewhale')}</h3>
        {windows ? (
          <p className='text-muted-foreground text-sm leading-6'>
            {t(
              'On Windows, install Codewhale from the official GitHub Releases page, then confirm that codewhale runs in your terminal.'
            )}
          </p>
        ) : (
          <InstallCommand
            value={CODEWHALE_UNIX_INSTALL_COMMAND}
            copyLabel={t('Copy Codewhale install command')}
          />
        )}
        <div className='flex flex-wrap gap-x-4 gap-y-2'>
          <a
            href={CODEWHALE_RELEASES}
            target='_blank'
            rel='noopener noreferrer'
            className='text-sm underline underline-offset-4'
          >
            {t('Codewhale releases')}
          </a>
          <a
            href={CODEWHALE_INSTALL_DOCS}
            target='_blank'
            rel='noopener noreferrer'
            className='text-sm underline underline-offset-4'
          >
            {t('Codewhale installation guide')}
          </a>
        </div>
      </div>

      <div className='space-y-3 border-t pt-5'>
        <div>
          <h3 className='text-sm font-semibold'>
            {t('2. Install the LMM companion adapter')}
          </h3>
          <p className='text-muted-foreground mt-1 text-sm leading-6'>
            {t(
              'The adapter requires Node.js 22 or newer. Clone the provider repository, enter it, then install the codewhale-lmm command globally.'
            )}
          </p>
        </div>
        <InstallCommand
          value={CODEWHALE_CLONE_COMMAND}
          copyLabel={t('Copy Codewhale adapter clone command')}
        />
        <InstallCommand
          value={CODEWHALE_ADAPTER_INSTALL_COMMAND}
          copyLabel={t('Copy Codewhale adapter install command')}
        />
      </div>

      <div className='space-y-3 border-t pt-5'>
        <div>
          <h3 className='text-sm font-semibold'>
            {t('3. Sign in, choose a model, and start')}
          </h3>
          <p className='text-muted-foreground mt-1 text-sm leading-6'>
            {t(
              'Run these commands in order. Login opens the LMM authorization page; models prints the account-scoped model IDs; run launches Codewhale through the temporary local provider.'
            )}
          </p>
        </div>
        <InstallCommand
          value={CODEWHALE_LOGIN_COMMAND}
          copyLabel={t('Copy Codewhale LMM login command')}
        />
        <InstallCommand
          value={CODEWHALE_MODELS_COMMAND}
          copyLabel={t('Copy Codewhale LMM models command')}
        />
        <InstallCommand
          value={CODEWHALE_RUN_COMMAND}
          copyLabel={t('Copy Codewhale LMM run command')}
        />
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'Use the complete model ID printed by codewhale-lmm models, including its group. Do not replace it with only the upstream model name.'
          )}
        </p>
      </div>

      <div className='space-y-2 border-t pt-5'>
        <h3 className='text-sm font-semibold'>{t('Useful commands')}</h3>
        <p className='text-muted-foreground text-sm leading-7'>
          <code>codewhale-lmm status</code>
          {' · '}
          <code>codewhale-lmm balance</code>
          {' · '}
          <code>codewhale-lmm usage</code>
          {' · '}
          <code>codewhale-lmm logout</code>
        </p>
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'The current preview supports LMM catalog entries declared as openai-completions. Responses-only and Anthropic Messages-only models are not supported by this adapter yet.'
          )}
        </p>
      </div>

      <a
        href={CODEWHALE_PROVIDER}
        target='_blank'
        rel='noopener noreferrer'
        className='text-sm underline underline-offset-4'
      >
        {t('Codewhale LMM adapter source and full documentation')}
      </a>
    </section>
  )
}
