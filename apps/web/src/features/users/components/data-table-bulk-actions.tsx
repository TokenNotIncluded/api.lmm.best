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
import type { Table } from '@tanstack/react-table'
import { Mail, UserRound } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { Separator } from '@/components/ui/separator'

import {
  collectContacts,
  collectEmails,
  collectUsernames,
  countMissingEmails,
  joinLines,
} from '../lib/bulk-selection'
import type { User } from '../types'

interface DataTableBulkActionsProps {
  table: Table<User>
}

/**
 * Bulk actions for the user table.
 *
 * Deliberately read-only: the admin API exposes no bulk mutation endpoint, so
 * every state-changing action stays on the per-row menu behind its own
 * confirmation dialog. What a selection can usefully do is hand the operator a
 * pasteable list, so the toolbar offers exactly that.
 */
export function DataTableBulkActions({ table }: DataTableBulkActionsProps) {
  const { t } = useTranslation()
  const selectedUsers = table
    .getFilteredSelectedRowModel()
    .rows.map((row) => row.original)

  const usernames = collectUsernames(selectedUsers)
  const emails = collectEmails(selectedUsers)
  const contacts = collectContacts(selectedUsers)
  const missingEmails = countMissingEmails(selectedUsers)

  const usernamesPayload = joinLines(usernames)
  const emailsPayload = joinLines(emails)
  const contactsPayload = joinLines(contacts)

  return (
    <BulkActionsToolbar table={table} entityName={t('user')}>
      <CopyButton
        value={usernamesPayload}
        variant='outline'
        size='sm'
        className='h-8 gap-1.5 px-2.5'
        tooltip={t('Copy usernames')}
        successTooltip={t('Usernames copied!')}
        aria-label={t('Copy usernames')}
      >
        <UserRound className='size-3.5' />
        <span className='hidden text-xs sm:inline'>{t('Copy usernames')}</span>
      </CopyButton>

      <CopyButton
        value={contactsPayload}
        variant='outline'
        size='sm'
        className='h-8 gap-1.5 px-2.5'
        tooltip={
          missingEmails > 0
            ? t('{{count}} selected user(s) have no email address', {
                count: missingEmails,
              })
            : t('Copy usernames and emails')
        }
        successTooltip={t('Contacts copied!')}
        aria-label={t('Copy usernames and emails')}
      >
        <Mail className='size-3.5' />
        <span className='hidden text-xs sm:inline'>
          {t('Copy usernames and emails')}
        </span>
      </CopyButton>

      {emails.length > 1 && (
        <>
          <Separator
            className='h-5'
            orientation='vertical'
            aria-hidden='true'
          />
          <CopyButton
            value={emailsPayload}
            variant='ghost'
            size='sm'
            className='h-8 gap-1.5 px-2.5'
            tooltip={t('Copy email addresses')}
            successTooltip={t('Emails copied!')}
            aria-label={t('Copy email addresses')}
          >
            <span className='hidden text-xs sm:inline'>
              {t('Copy email addresses')}
            </span>
          </CopyButton>
        </>
      )}
    </BulkActionsToolbar>
  )
}
