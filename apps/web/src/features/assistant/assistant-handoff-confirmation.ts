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
export const maxAssistantHandoffCharacters = 2000

type PreparedHandoff = {
  confirmation_token: string
  message: string
}

export type AssistantHandoffConfirmation = Readonly<{
  message: string
  confirmationToken?: string
}>

export function isValidAssistantHandoffMessage(message: string): boolean {
  const length = [...message.trim()].length
  return (
    length >= minAssistantHandoffCharacters &&
    length <= maxAssistantHandoffCharacters
  )
}

// An unusable prepared action must not lock the message into a read-only form.
// Manual edits require a new review and must never reuse the original token.
export function getAssistantHandoffConfirmationToken(
  action: PreparedHandoff | null | undefined
): string | undefined {
  if (
    !action ||
    typeof action.confirmation_token !== 'string' ||
    !action.confirmation_token.trim() ||
    typeof action.message !== 'string' ||
    !isValidAssistantHandoffMessage(action.message)
  ) {
    return undefined
  }
  return action.confirmation_token
}

export function createAssistantHandoffConfirmation(
  message: string,
  action?: PreparedHandoff | null
): AssistantHandoffConfirmation | null {
  const trimmedMessage = message.trim()
  if (!isValidAssistantHandoffMessage(trimmedMessage)) return null
  const confirmationToken = getAssistantHandoffConfirmationToken(action)
  // The server submits the token-bound message, not the supplied message.
  // Never let the visible preview disagree with that immutable server action.
  if (confirmationToken && trimmedMessage !== action?.message.trim()) {
    return null
  }
  return Object.freeze({ message: trimmedMessage, confirmationToken })
}

export function isSameAssistantHandoffConfirmation(
  left: AssistantHandoffConfirmation | null,
  right: AssistantHandoffConfirmation | null
): boolean {
  return Boolean(
    left &&
    right &&
    left.message === right.message &&
    left.confirmationToken === right.confirmationToken
  )
}
