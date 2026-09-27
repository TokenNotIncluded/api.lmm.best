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
import type { ApiKey, ApiKeyCreationMode } from '../types'

export type ApiKeyCreationSource =
  | 'manual'
  | 'legacy'
  | 'system'
  | 'drawing_mcp'
  | 'assistant'
  | 'assistant_runtime'
  | 'red_packet_cover'
  | string

export function isAssistantRuntimeKey(
  apiKey: Pick<ApiKey, 'creation_source' | 'source'>
): boolean {
  return getApiKeyCreationSource(apiKey) === 'assistant_runtime'
}

export function getApiKeyCreationSource(
  apiKey: Pick<ApiKey, 'creation_source' | 'source'>
): ApiKeyCreationSource {
  const source = (apiKey.creation_source ?? apiKey.source)?.trim().toLowerCase()
  return source || 'legacy'
}

export function getApiKeyCreationMode(
  apiKey: Pick<ApiKey, 'creation_source' | 'source'>
): ApiKeyCreationMode {
  const source = getApiKeyCreationSource(apiKey)
  return source === 'manual' || source === 'legacy' ? 'manual' : 'automatic'
}

export function matchesApiKeyCreationMode(
  apiKey: Pick<ApiKey, 'creation_source' | 'source'>,
  mode: ApiKeyCreationMode
): boolean {
  return getApiKeyCreationMode(apiKey) === mode
}
