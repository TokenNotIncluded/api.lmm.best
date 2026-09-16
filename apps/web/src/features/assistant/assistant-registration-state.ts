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
/*
Copyright (C) 2026 LIghtJUNction
*/
export type RegistrationState =
  | 'context_needed'
  | 'ready'
  | 'held'
  | 'suspended'
  | 'active'

export function registrationState(value: unknown): RegistrationState {
  if (
    value === 'ready' ||
    value === 'held' ||
    value === 'suspended' ||
    value === 'active'
  ) {
    return value
  }
  return 'context_needed'
}

export function registrationStateCopy(state: RegistrationState) {
  switch (state) {
    case 'active':
      return {
        title: 'L1 access is active',
        detail: 'You can continue setting up your client.',
      }
    case 'ready':
      return {
        title: 'Continue with the assistant',
        detail:
          'The assistant can complete access verification during this conversation. No recommendation letter is required.',
      }
    case 'held':
      return {
        title: 'Registration needs another check',
        detail:
          'Access and welcome rewards are on hold. You can keep asking questions or request human support.',
      }
    case 'suspended':
      return {
        title: 'Account access is paused',
        detail:
          'An administrator can review the recorded action. A pause is not a judgment about your writing style.',
      }
    default:
      return {
        title: 'Tell us what you need',
        detail:
          'A brief description in your own words is enough to start. There is no difficult puzzle or application letter.',
      }
  }
}
