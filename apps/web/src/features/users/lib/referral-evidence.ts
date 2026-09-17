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
export const REFERRAL_EVIDENCE_MAX_CHARACTERS = 1000

/** Match Go strings.TrimSpace and PostgreSQL's Unicode-character length.
 * Do not normalize combining marks or truncate the evidence kept for retries.
 */
export function inspectReferralEvidence(input: string) {
  const value = input.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '')
  const length = Array.from(value).length
  const valid =
    length > 0 &&
    length <= REFERRAL_EVIDENCE_MAX_CHARACTERS &&
    !value.includes('\0') &&
    // With the u flag, paired surrogates are one non-BMP code point.
    !/[\uD800-\uDFFF]/u.test(value)
  return { value, length, valid }
}
