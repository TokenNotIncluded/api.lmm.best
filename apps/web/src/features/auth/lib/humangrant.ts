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
// Compatibility layer for main's HumanGrant API. The server remains the authority.
// Use raw same-origin fetch: dashboard authorization interceptors are not applicable.
export const HUMAN_GRANT_CONFIG = '/humangrant/config'
export const HUMAN_GRANT_SETTINGS = '/api/user/auth/humangrant/settings'

export type HumanGrantSettings = Readonly<{
  enabled: boolean
  csrf: string
}>

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function isHumanGrantAvailable(value: unknown): boolean {
  return isRecord(value) && value.enabled === true && value.ready === true
}

export function parseHumanGrantSettings(value: unknown): HumanGrantSettings {
  if (
    !isRecord(value) ||
    typeof value.enabled !== 'boolean' ||
    typeof value.csrf !== 'string' ||
    value.csrf.trim().length === 0
  ) {
    throw new Error('Invalid HumanGrant settings')
  }
  return { enabled: value.enabled, csrf: value.csrf }
}

export async function readHumanGrantSettings(
  signal: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<HumanGrantSettings> {
  const response = await fetcher(HUMAN_GRANT_SETTINGS, {
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    signal,
  })
  if (!response.ok) throw new Error('HumanGrant settings unavailable')
  return parseHumanGrantSettings(await response.json())
}

// One PUT only: transport failure does not prove the server rejected the write.
// After successful acknowledgement, read back state and obtain a fresh proof.
export async function writeHumanGrantSettings(
  settings: HumanGrantSettings,
  signal: AbortSignal,
  fetcher: typeof fetch = fetch
): Promise<HumanGrantSettings> {
  const validated = parseHumanGrantSettings(settings)
  const response = await fetcher(HUMAN_GRANT_SETTINGS, {
    method: 'PUT',
    credentials: 'same-origin',
    cache: 'no-store',
    redirect: 'error',
    signal,
    headers: {
      'Content-Type': 'application/json',
      'X-HG-CSRF': validated.csrf,
    },
    body: JSON.stringify({ enabled: validated.enabled }),
  })
  if (!response.ok) throw new Error('HumanGrant write not confirmed')
  const current = await readHumanGrantSettings(signal, fetcher)
  if (current.enabled !== validated.enabled) {
    throw new Error('HumanGrant settings changed during save')
  }
  return current
}

// A slow earlier response must not restore a button hidden by a newer response.
// Abort alone is insufficient: parsing can finish after the abort signal.
export function watchHumanGrantAvailability(
  onChange: (available: boolean) => void,
  fetcher: typeof fetch = fetch
) {
  let sequence = 0
  let disposed = false
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined

  function cancelPending() {
    controller?.abort()
    clearTimeout(timer)
  }

  async function refresh(): Promise<void> {
    if (disposed) return
    const request = ++sequence
    cancelPending()
    const active = new AbortController()
    controller = active
    onChange(false)
    timer = setTimeout(() => active.abort(), 10_000)
    try {
      const response = await fetcher(HUMAN_GRANT_CONFIG, {
        credentials: 'omit',
        cache: 'no-store',
        redirect: 'error',
        signal: active.signal,
      })
      const value: unknown = response.ok ? await response.json() : null
      if (!disposed && request === sequence && !active.signal.aborted) {
        onChange(isHumanGrantAvailable(value))
      }
    } catch {
      // A fresh check has already hidden the entry; do not restore old state.
    } finally {
      if (request === sequence) clearTimeout(timer)
    }
  }

  return {
    refresh,
    dispose() {
      disposed = true
      sequence += 1
      cancelPending()
    },
  }
}
