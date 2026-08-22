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
import type { BackendCapabilities, SystemStatus } from '@/features/auth/types'

const NO_BACKEND_CAPABILITIES: Readonly<BackendCapabilities> = Object.freeze({
  bounty_notifications: false,
  bounty_challenge_cancel: false,
  bounty_public_read: false,
  self_oauth_unbind: false,
  responses_websocket: false,
})

export function getBackendCapabilities(
  status: SystemStatus | null | undefined
): BackendCapabilities {
  const reported =
    status?.backend_capabilities ?? status?.data?.backend_capabilities

  return {
    bounty_notifications: reported?.bounty_notifications === true,
    bounty_challenge_cancel: reported?.bounty_challenge_cancel === true,
    bounty_public_read: reported?.bounty_public_read === true,
    self_oauth_unbind: reported?.self_oauth_unbind === true,
    responses_websocket: reported?.responses_websocket === true,
  }
}

export function normalizeBackendCapabilities(
  status: SystemStatus,
  trustReportedCapabilities = true
): SystemStatus {
  return {
    ...status,
    backend_capabilities: trustReportedCapabilities
      ? getBackendCapabilities(status)
      : NO_BACKEND_CAPABILITIES,
  }
}

export function getCapabilitySafeStatus(
  status: SystemStatus | null | undefined,
  liveStatusConfirmed: boolean
): SystemStatus | null {
  if (!status) return null
  return normalizeBackendCapabilities(status, liveStatusConfirmed)
}
