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
type DeletePlanHttpError = {
  response?: {
    status?: number
    data?: { message?: unknown }
  }
  message?: unknown
}

export function getDeletePlanErrorMessage(
  error: unknown,
  fallback: string
): string {
  if (typeof error !== 'object' || error === null) return fallback

  const httpError = error as DeletePlanHttpError
  const backendMessage = httpError.response?.data?.message
  if (typeof backendMessage === 'string' && backendMessage.trim()) {
    return backendMessage
  }

  const status = httpError.response?.status
  if (typeof status === 'number') return `${fallback} (${status})`
  return typeof httpError.message === 'string' && httpError.message.trim()
    ? httpError.message
    : fallback
}
