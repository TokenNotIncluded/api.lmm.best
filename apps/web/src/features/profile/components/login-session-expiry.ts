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
const weekSeconds = 7 * 24 * 60 * 60

interface SessionExpiry {
  created_at: number
  expires_at: number
}

// Go revokes when created_at < now - seven days. The first invalid Unix
// second is therefore created_at + seven days + 1, not last_active_at.
export function loginSessionExpiresAt(
  session: SessionExpiry,
  sessionAutoLogout = true
): number {
  const weeklyExpiry = session.created_at + weekSeconds + 1
  if (
    !sessionAutoLogout ||
    !Number.isSafeInteger(session.created_at) ||
    session.created_at < 0 ||
    !Number.isSafeInteger(weeklyExpiry)
  ) {
    return session.expires_at
  }
  return Math.min(session.expires_at, weeklyExpiry)
}
