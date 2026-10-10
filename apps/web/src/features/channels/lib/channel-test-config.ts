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
import { CHANNEL_TYPE_TYPESAFE } from '../constants'

const ENDPOINT_TYPE_OPTIONS = [
  { value: 'auto', label: 'Auto detect (default)' },
  { value: 'openai', label: 'OpenAI (/v1/chat/completions)' },
  { value: 'openai-response', label: 'OpenAI Responses (/v1/responses)' },
  {
    value: 'openai-response-compact',
    label: 'OpenAI Response Compaction (/v1/responses/compact)',
  },
  { value: 'anthropic', label: 'Anthropic (/v1/messages)' },
  {
    value: 'gemini',
    label: 'Gemini (/v1beta/models/{model}:generateContent)',
  },
  { value: 'jina-rerank', label: 'Jina Rerank (/v1/rerank)' },
  {
    value: 'image-generation',
    label: 'Image Generation (/v1/images/generations)',
  },
  { value: 'embeddings', label: 'Embeddings (/v1/embeddings)' },
  { value: 'moderation', label: 'Moderation (/v1/moderations)' },
  {
    value: 'systemone',
    label: 'TypeSafe Judgment (/typesafe/v1/systemone)',
  },
]

const STREAM_INCOMPATIBLE_ENDPOINTS = new Set([
  'embeddings',
  'image-generation',
  'jina-rerank',
  'openai-response-compact',
  'systemone',
  'moderation',
])

export function getChannelTestEndpointOptions(type: number) {
  return type === CHANNEL_TYPE_TYPESAFE
    ? ENDPOINT_TYPE_OPTIONS.filter((option) =>
        ['auto', 'systemone'].includes(option.value)
      )
    : ENDPOINT_TYPE_OPTIONS
}

export function getDefaultChannelTestEndpoint(type: number): string {
  return type === CHANNEL_TYPE_TYPESAFE ? 'systemone' : 'auto'
}

export function supportsChannelStreamTest(
  type: number,
  endpointType: string
): boolean {
  return (
    type !== CHANNEL_TYPE_TYPESAFE &&
    !STREAM_INCOMPATIBLE_ENDPOINTS.has(endpointType)
  )
}
