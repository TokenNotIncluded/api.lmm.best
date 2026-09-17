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
/** Keep one primitive-valued payload and request ID for the entire dialog.
 * An API error can happen after the database committed, so neither a network
 * failure nor success:false authorizes issuing a fresh financial operation.
 */
export function createReferralSubmission<T extends { request_id: string }>() {
  let retained: Readonly<T> | undefined
  return (create: () => T): Readonly<T> => {
    if (!retained) {
      const proposed = create()
      if (!proposed.request_id.trim()) {
        throw new Error('A stable referral request ID is required')
      }
      retained = Object.freeze({ ...proposed })
    }
    return retained
  }
}
