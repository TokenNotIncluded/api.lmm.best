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

export const minAssistantHandoffCharacters = 5

type PreparedHandoff = {
  confirmation_token: string
  message: string
}

// An unusable prepared action must not lock the message into a read-only form.
// The user can instead edit and explicitly confirm a manual support message;
// the original action token must never be attached to that edited message.
export function getAssistantHandoffConfirmationToken(
  action: PreparedHandoff | null | undefined
): string | undefined {
  if (
    !action ||
    typeof action.confirmation_token !== 'string' ||
    !action.confirmation_token.trim() ||
    typeof action.message !== 'string' ||
    [...action.message.trim()].length < minAssistantHandoffCharacters
  ) {
    return undefined
  }
  return action.confirmation_token
}
