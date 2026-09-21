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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { SourceQuestionnaire } from '@/features/acquisition/source-questionnaire'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { formatQuota } from '@/lib/format'

import { AccessRequestDetails } from './access-request-details'
import { useAccountNextStep } from './use-account-next-step'

export function AccountStatus({
  showRequestDetails = false,
}: {
  showRequestDetails?: boolean
}) {
  const { t } = useTranslation()
  const { user, request, nextStep } = useAccountNextStep()
  if (!user) return null
  const granted = user.developer_access_granted === true
  const access = granted
    ? 'API access enabled'
    : request.isError
      ? 'Unable to load access status'
      : !request.isSuccess
        ? 'Loading'
        : request.data?.status === 'pending'
          ? 'Pending review'
          : request.data?.status === 'rejected'
            ? 'Access request rejected'
            : request.data?.status === 'approved'
              ? 'Refresh account status'
              : 'Not requested'
  const details =
    user.onboarding?.details_available === false ? undefined : user.onboarding
  const keyCreated = details?.api_key_created
  const firstRequest = details?.first_request_complete
  const facts = [
    [t('API access'), t(access)],
    [
      t('API Key'),
      t(
        keyCreated === undefined
          ? 'Unknown'
          : keyCreated
            ? 'Created'
            : 'Not created'
      ),
    ],
    [
      t('Balance'),
      user.quota === undefined ? t('Unknown') : formatQuota(user.quota),
    ],
    [
      t('First successful request'),
      t(
        firstRequest === undefined
          ? 'Unknown'
          : firstRequest
            ? 'Completed'
            : 'Not completed'
      ),
    ],
  ]
  return (
    <section
      aria-label={t('Account status')}
      className='space-y-4 border-b pb-6'
    >
      <h2 className='text-base font-semibold'>{t('Account status')}</h2>
      <dl className='grid grid-cols-2 gap-4 sm:grid-cols-4'>
        {facts.map(([label, value]) => (
          <div key={label} className='min-w-0'>
            <dt className='text-muted-foreground text-sm'>{label}</dt>
            <dd className='mt-1 text-sm font-medium break-words'>{value}</dd>
          </div>
        ))}
      </dl>
      {showRequestDetails && <AccessRequestDetails key={user.id} />}
      {request.isError && !granted ? (
        <Button
          variant='outline'
          onClick={() => void request.refetch()}
          disabled={request.isFetching}
        >
          {t('Reload account status')}
        </Button>
      ) : showRequestDetails && !granted ? (
        <Button onClick={() => requestAssistantOpen('onboarding')}>
          {t('Start with AI assistant')}
        </Button>
      ) : (
        <Button render={<Link to={nextStep.to} />}>{t(nextStep.label)}</Button>
      )}
      {showRequestDetails && <SourceQuestionnaire />}
    </section>
  )
}
