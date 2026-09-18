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
import type { ApiKeyGroupOption } from '../components/api-key-group-combobox'

/**
 * Map the `/api/user/self/groups` response shape into combobox options.
 * Shared by the edit drawer's group field and the quick group-switch
 * control in the API keys table so both stay in sync with a single mapping.
 */
export function buildApiKeyGroupOptions(
  groupsData:
    | Record<
        string,
        {
          desc?: string
          ratio?: number | string
          warning?: ApiKeyGroupOption['warning']
        }
      >
    | undefined
): ApiKeyGroupOption[] {
  return Object.entries(groupsData || {}).map(([key, info]) => ({
    value: key,
    label: key,
    desc: info.desc || key,
    ratio: info.ratio,
    warning: info.warning,
  }))
}
