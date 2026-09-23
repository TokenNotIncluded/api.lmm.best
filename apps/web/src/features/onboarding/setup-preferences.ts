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
const STORAGE_KEY = 'lmm:setup-preferences:v1'
const PLATFORMS = ['windows', 'macos', 'linux', 'android', 'ios'] as const
const CLIENTS = [
  'pi',
  'dsh',
  'astrbot',
  'openai-sdk',
  'anthropic-sdk',
  'cherry-studio',
  'chatbox',
  'claude-code',
  'cc-switch',
  'claude-desktop',
  'chatgpt',
  'codex',
  'openai-compatible',
] as const

export type SetupPreferences = {
  platform: (typeof PLATFORMS)[number]
  client: (typeof CLIENTS)[number]
}

/** Device/client preferences only. Never persist credentials or account progress. */
export function readSetupPreferences(): SetupPreferences | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(STORAGE_KEY) ?? 'null')
    if (
      !value ||
      !PLATFORMS.includes(value.platform) ||
      !CLIENTS.includes(value.client)
    ) {
      return null
    }
    if (
      ['android', 'ios'].includes(value.platform) &&
      !['chatbox', 'chatgpt'].includes(value.client)
    ) {
      return null
    }
    return { platform: value.platform, client: value.client }
  } catch {
    return null
  }
}

export function saveSetupPreferences(value: SetupPreferences) {
  try {
    sessionStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ platform: value.platform, client: value.client })
    )
  } catch {
    // Private browsing or blocked storage must not prevent setup.
  }
}
