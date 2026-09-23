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
  DSH_DOWNLOAD_LATEST_COMMAND,
  DSH_PACKAGE_LATEST,
  DSH_WEB_INSTALL_LATEST_COMMAND,
} from './provider-install-commands'

export function DshOAuthGuide() {
  const { t } = useTranslation()

  return (
    <section id='dsh-oauth' className='scroll-mt-24 space-y-5 border-y py-6'>
      <div>
        <h2 className='text-xl font-semibold'>{t('Use LMM in DSH Desktop')}</h2>
        <p className='text-muted-foreground mt-2 text-sm leading-7'>
          {t(
            'In official DSH Desktop, open Plugins → Add plugin and paste this package name. A terminal command cannot install into the Desktop profile.'
          )}
        </p>
      </div>

      <div className='space-y-2'>
        <h3 className='text-sm font-semibold'>
          {t('Package name for Desktop Plugins')}
        </h3>
        <InstallCommand
          value={DSH_PACKAGE_LATEST}
          copyLabel={t('Copy DSH package name')}
        />
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'Remove any existing local, Git, or archive copy in Desktop Plugins before adding the npm package. Then open Settings → Models, sign in with LMM, and choose Continue in the browser where you are already logged in.'
          )}
        </p>
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'The current latest plugin is for DSH Desktop 0.1.7-alpha.2. DSH 0.1.5-rc.2 needs plugin 0.1.0-alpha.3 instead.'
          )}
        </p>
      </div>

      <div className='space-y-2 border-t pt-5'>
        <h3 className='text-sm font-semibold'>{t('DSH Web CLI')}</h3>
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'For dsh web in a terminal, run this command. It installs only into the web profile.'
          )}
        </p>
        <InstallCommand
          value={DSH_WEB_INSTALL_LATEST_COMMAND}
          copyLabel={t('Copy DSH Web install command')}
        />
      </div>

      <div className='space-y-2 border-t pt-5'>
        <h3 className='text-sm font-semibold'>
          {t('Download latest package')}
        </h3>
        <p className='text-muted-foreground text-sm leading-6'>
          {t(
            'This command downloads the latest .tgz archive to the current folder. Downloading alone does not install the Desktop plugin.'
          )}
        </p>
        <InstallCommand
          value={DSH_DOWNLOAD_LATEST_COMMAND}
          copyLabel={t('Copy DSH download command')}
        />
      </div>

      <a
        href='https://www.npmjs.com/package/@tokennotincluded/dsh-lmm-provider'
        target='_blank'
        rel='noopener noreferrer'
        className='text-sm underline underline-offset-4'
      >
        {t('DSH plugin package')}
      </a>
    </section>
  )
}
