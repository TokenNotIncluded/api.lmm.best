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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { ApiBaseUrl } from './components/api-base-url'
import { ApiKeysDialogs } from './components/api-keys-dialogs'
import { ApiKeysPrimaryButtons } from './components/api-keys-primary-buttons'
import { ApiKeysProvider } from './components/api-keys-provider'
import { ApiKeysTable } from './components/api-keys-table'
import { AutomaticApiKeyActions } from './components/automatic-api-key-actions'
import type { ApiKeyCreationMode } from './types'

export function ApiKeys() {
  const { t } = useTranslation()
  const [creationMode, setCreationMode] = useState<ApiKeyCreationMode>('manual')
  return (
    <ApiKeysProvider>
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('API Keys')}</SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          {creationMode === 'manual' ? <ApiKeysPrimaryButtons /> : null}
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <ApiBaseUrl />
          <Tabs
            value={creationMode}
            onValueChange={(value) =>
              setCreationMode(value as ApiKeyCreationMode)
            }
          >
            <TabsList aria-label={t('API key creation mode')}>
              <TabsTrigger value='manual'>{t('Manual creation')}</TabsTrigger>
              <TabsTrigger value='automatic'>
                {t('Automatic creation')}
              </TabsTrigger>
            </TabsList>
            <TabsContent value={creationMode} className='pt-3'>
              <ApiKeysTable creationMode={creationMode} />
              {creationMode === 'automatic' ? (
                <div className='mt-5'>
                  <AutomaticApiKeyActions />
                </div>
              ) : null}
            </TabsContent>
          </Tabs>
        </SectionPageLayout.Content>
      </SectionPageLayout>

      <ApiKeysDialogs />
    </ApiKeysProvider>
  )
}
