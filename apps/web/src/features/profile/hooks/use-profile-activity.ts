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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getProfileUsageWindow } from '../api'
import {
  buildProfileUsageQueryRanges,
  getProfileActivityRange,
} from '../lib/activity'

export function useProfileActivity(
  accountCreatedTime?: number,
  enabled = true
) {
  const range = useMemo(() => getProfileActivityRange(), [])
  const queryStart = Math.max(
    range.start_timestamp,
    Number(accountCreatedTime) || range.start_timestamp
  )
  const queryRanges = useMemo(
    () => buildProfileUsageQueryRanges(queryStart, range.end_timestamp),
    [queryStart, range.end_timestamp]
  )

  const query = useQuery({
    queryKey: ['profile', 'token-activity', queryStart, range.end_timestamp],
    queryFn: async () => {
      const windows = await Promise.all(
        queryRanges.map((window) => getProfileUsageWindow(window))
      )
      return windows.flat()
    },
    enabled,
    staleTime: 5 * 60 * 1000,
    retry: 1,
  })

  return { ...query, range }
}
