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
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import { getApiKeyCreationSource } from '../lib'
import type { ApiKey } from '../types'

const SOURCE_LABELS: Record<string, string> = {
  manual: 'Manual',
  system: 'System',
  legacy: 'Legacy',
  drawing_mcp: 'Drawing MCP',
  assistant: 'Created with assistant',
  assistant_runtime: 'AI assistant runtime',
  red_packet_cover: 'Red packet cover',
}

export function ApiKeyCreationSourceBadge({ apiKey }: { apiKey: ApiKey }) {
  const { t } = useTranslation()
  const source = getApiKeyCreationSource(apiKey)
  const knownLabel = SOURCE_LABELS[source]

  return (
    <Badge variant='secondary' className='max-w-44 truncate'>
      {knownLabel ? t(knownLabel) : source}
    </Badge>
  )
}
