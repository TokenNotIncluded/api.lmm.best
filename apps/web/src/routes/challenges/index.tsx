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
import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { ChallengeList } from '@/features/forge/challenge-list'
import { EcosystemRouteShell } from '@/features/forge/ecosystem-route-shell'

export const Route = createFileRoute('/challenges/')({
  component: ChallengesPage,
})

function ChallengesPage() {
  const { t } = useTranslation()

  return (
    <EcosystemRouteShell
      console={
        <SectionPageLayout>
          <SectionPageLayout.Title>{t('Challenges')}</SectionPageLayout.Title>
          <SectionPageLayout.Content>
            <div className='mx-auto w-full max-w-5xl'>
              <ChallengeList showHeading={false} console />
            </div>
          </SectionPageLayout.Content>
        </SectionPageLayout>
      }
      public={
        <main className='mx-auto max-w-7xl px-5 pt-12 pb-20 md:px-10 md:pt-16'>
          <ChallengeList />
        </main>
      }
    />
  )
}
