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
import { isAxiosError } from 'axios'

import { api } from '@/lib/api'

import type { SetupFormValues, SetupResponse } from './types'

export async function getSetupStatus(): Promise<SetupResponse> {
  const res = await api.get('/api/setup', {
    // We want fresh status on every visit.
    params: {
      t: Date.now(),
    },
    // Root bootstrap and the setup screen both render their own fallback.
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  const status = res.data?.data
  if (
    res.data?.success !== true ||
    typeof status?.status !== 'boolean' ||
    typeof status?.root_init !== 'boolean' ||
    typeof status?.database_type !== 'string' ||
    !status.database_type.trim() ||
    (status.status && !status.root_init) ||
    (status.initialization_method !== undefined &&
      status.initialization_method !== 'cli')
  ) {
    throw new Error('Invalid setup status response')
  }
  return res.data
}

export async function submitSetup(
  payload: Record<string, unknown>
): Promise<SetupResponse> {
  const res = await api.post('/api/setup', payload, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return res.data
}

export function isCliOnlySetupError(error: unknown): boolean {
  return (
    isAxiosError(error) &&
    error.response?.status === 403 &&
    error.response.data?.code === 'SETUP_CLI_ONLY'
  )
}

export function buildSetupPayload(
  values: SetupFormValues,
  rootInitialized: boolean
) {
  const { usageMode, ...rest } = values

  const basePayload = {
    SelfUseModeEnabled: usageMode === 'self',
    DemoSiteEnabled: usageMode === 'demo',
  }

  if (rootInitialized) {
    return {
      username: rest.username,
      password: rest.password,
      ...basePayload,
    }
  }

  return {
    ...rest,
    ...basePayload,
  }
}
