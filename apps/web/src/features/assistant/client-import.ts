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
import { buildCCSwitchProviderURL } from '@/lib/cc-switch-deep-link'

export type AssistantImportApp = 'claude' | 'codex'

// Protocol verified against the client's own documentation:
// https://github.com/farion1231/cc-switch/blob/main/docs/user-manual/en/5-faq/5.3-deeplink.md
// Only invoke this from an explicit browser click. Never persist or put the
// resulting credential-bearing URL into assistant messages or HTTP requests.
export function buildAssistantClientImport(options: {
  app: AssistantImportApp
  rootUrl: string
  openAIBaseUrl: string
  currentOrigin: string
  model: string
  availableModels: string[]
  apiKey: string
}): string | null {
  const key = options.apiKey.trim()
  if (
    !['claude', 'codex'].includes(options.app) ||
    !key ||
    key.length > 512 ||
    /\s/.test(key) ||
    key.includes('<') ||
    key.includes('>') ||
    !options.model ||
    options.model === '<MODEL_ID>' ||
    !options.availableModels.includes(options.model)
  ) {
    return null
  }

  try {
    const root = new URL(options.rootUrl)
    const base = new URL(options.openAIBaseUrl)
    for (const url of [root, base]) {
      if (
        url.protocol !== 'https:' ||
        url.origin !== options.currentOrigin ||
        url.username ||
        url.password ||
        url.search ||
        url.hash
      ) {
        return null
      }
    }
    const serviceRoot = root.toString().replace(/\/+$/, '')
    const baseUrl = base.toString().replace(/\/+$/, '')
    if (baseUrl !== `${serviceRoot}/v1`) return null
    return buildCCSwitchProviderURL({
      app: options.app,
      name: 'LMM',
      endpoint: options.app === 'codex' ? baseUrl : serviceRoot,
      apiKey: key,
      models: { model: options.model },
      homepage: serviceRoot,
      // Leave activation to the client after its import confirmation.
      enabled: false,
    })
  } catch {
    return null
  }
}
