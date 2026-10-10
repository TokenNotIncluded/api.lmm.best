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
import { createFileRoute, redirect } from '@tanstack/react-router'
import z from 'zod'

import { Users } from '@/features/users'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const usersSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(undefined),
  filter: z.string().optional().catch(''),
  status: z
    .array(z.enum(['-1', '1', '2']))
    .optional()
    .catch([]),
  role: z
    .array(z.enum(['1', '10', '100']))
    .optional()
    .catch([]),
  group: z.string().optional().catch(''),
  l0Only: z.boolean().default(false).catch(false),
  sortBy: z
    .enum([
      'id',
      'username',
      'quota',
      'group',
      'created_at',
      'last_login_at',
      'topup_quota',
      'topup_money',
      'assistant_violations',
      'risk_score',
      'transferred_quota',
      'received_quota',
      'checkin_quota',
      'used_quota',
      'request_count',
    ])
    .default('id')
    .catch('id'),
  sortOrder: z.enum(['asc', 'desc']).default('desc').catch('desc'),
  risk: z.enum(['all', 'high', 'medium', 'low']).default('all').catch('all'),
  transfers: z
    .enum(['all', 'sent', 'received', 'none'])
    .default('all')
    .catch('all'),
  usage: z.enum(['all', 'zero', 'consumed']).default('all').catch('all'),
  funding: z.enum(['all', 'paid', 'unpaid']).default('all').catch('all'),
  checkin: z.enum(['all', 'yes', 'no']).default('all').catch('all'),
})

export const Route = createFileRoute('/_authenticated/users/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/403',
      })
    }
  },
  validateSearch: usersSearchSchema,
  component: Users,
})
