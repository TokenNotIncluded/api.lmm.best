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
import { installPersonaDebugRuntime } from '@/features/debug/persona-runtime'
import { api } from '@/lib/http-client'

installPersonaDebugRuntime()

// Navigation can preload the public scripts page. Keep this empty read fixture
// in the development entry, never in the production app or network adapter.
const personaAdapter = api.defaults.adapter
if (typeof personaAdapter !== 'function') {
  throw new Error('Persona debug adapter was not installed')
}
api.defaults.adapter = async (config) => {
  const url = new URL(config.url ?? '', window.location.origin)
  if (
    (config.method ?? 'get').toUpperCase() === 'GET' &&
    url.origin === window.location.origin &&
    url.pathname === '/api/scripts'
  ) {
    return {
      data: { success: true, data: [] },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  return personaAdapter(config)
}

void import('./main')
